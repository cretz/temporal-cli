package temporalcli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"strings"

	"go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func (c *ClientOptions) dialClient(cctx *CommandContext) (client.Client, error) {
	if cctx.RootCommand == nil {
		return nil, fmt.Errorf("root command unexpectedly missing when dialing client")
	}

	// Load a client config profile
	var clientProfile envconfig.ClientConfigProfile
	if !cctx.RootCommand.DisableConfigFile || !cctx.RootCommand.DisableConfigEnv {
		var err error
		clientProfile, err = envconfig.LoadClientConfigProfile(envconfig.LoadClientConfigProfileOptions{
			ConfigFilePath:    cctx.RootCommand.ConfigFile,
			ConfigFileProfile: cctx.RootCommand.Profile,
			DisableFile:       cctx.RootCommand.DisableConfigFile,
			DisableEnv:        cctx.RootCommand.DisableConfigEnv,
			EnvLookup:         cctx.Options.EnvLookup,
		})
		if err != nil {
			return nil, fmt.Errorf("failed loading client config: %w", err)
		}
	}

	// To support legacy TLS environment variables, if they are present, we will
	// have them force-override anything loaded from existing file or env
	if !cctx.RootCommand.DisableConfigEnv {
		oldEnvTLSCert, _ := cctx.Options.EnvLookup.LookupEnv("TEMPORAL_TLS_CERT")
		oldEnvTLSCertData, _ := cctx.Options.EnvLookup.LookupEnv("TEMPORAL_TLS_CERT_DATA")
		oldEnvTLSKey, _ := cctx.Options.EnvLookup.LookupEnv("TEMPORAL_TLS_KEY")
		oldEnvTLSKeyData, _ := cctx.Options.EnvLookup.LookupEnv("TEMPORAL_TLS_KEY_DATA")
		oldEnvTLSCA, _ := cctx.Options.EnvLookup.LookupEnv("TEMPORAL_TLS_CA")
		oldEnvTLSCAData, _ := cctx.Options.EnvLookup.LookupEnv("TEMPORAL_TLS_CA_DATA")
		if oldEnvTLSCert != "" || oldEnvTLSCertData != "" ||
			oldEnvTLSKey != "" || oldEnvTLSKeyData != "" ||
			oldEnvTLSCA != "" || oldEnvTLSCAData != "" {
			if clientProfile.TLS == nil {
				clientProfile.TLS = &envconfig.ClientConfigTLS{}
			}
			if oldEnvTLSCert != "" {
				clientProfile.TLS.ClientCertPath = oldEnvTLSCert
			}
			if oldEnvTLSCertData != "" {
				clientProfile.TLS.ClientCertData = []byte(oldEnvTLSCertData)
			}
			if oldEnvTLSKey != "" {
				clientProfile.TLS.ClientKeyPath = oldEnvTLSKey
			}
			if oldEnvTLSKeyData != "" {
				clientProfile.TLS.ClientKeyData = []byte(oldEnvTLSKeyData)
			}
			if oldEnvTLSCA != "" {
				clientProfile.TLS.ServerCACertPath = oldEnvTLSCA
			}
			if oldEnvTLSCAData != "" {
				clientProfile.TLS.ServerCACertData = []byte(oldEnvTLSCAData)
			}
		}
	}

	// Override some values in client config profile that come from CLI args
	if c.Address != "" {
		clientProfile.Address = c.Address
	}
	if c.Namespace != "" {
		clientProfile.Namespace = c.Namespace
	}
	if c.ApiKey != "" {
		clientProfile.APIKey = c.ApiKey
	}
	if len(c.GrpcMeta) > 0 {
		// We append meta, not override
		if len(clientProfile.GRPCMeta) == 0 {
			clientProfile.GRPCMeta = make(map[string]string, len(c.GrpcMeta))
		}
		for _, kv := range c.GrpcMeta {
			pieces := strings.SplitN(kv, "=", 2)
			if len(pieces) != 2 {
				return nil, fmt.Errorf("gRPC meta of %q does not have '='", kv)
			}
			clientProfile.GRPCMeta[pieces[0]] = pieces[1]
		}
	}

	// If any of these values are present, set TLS if not set, and set values.
	// NOTE: This means that tls=false does not explicitly disable TLS when set
	// via envconfig.
	if c.Tls ||
		c.TlsCertPath != "" || c.TlsKeyPath != "" || c.TlsCaPath != "" ||
		c.TlsCertData != "" || c.TlsKeyData != "" || c.TlsCaData != "" {
		if clientProfile.TLS == nil {
			clientProfile.TLS = &envconfig.ClientConfigTLS{}
		}
		if c.TlsCertPath != "" {
			clientProfile.TLS.ClientCertPath = c.TlsCertPath
		}
		if c.TlsCertData != "" {
			clientProfile.TLS.ClientCertData = []byte(c.TlsCertData)
		}
		if c.TlsKeyPath != "" {
			clientProfile.TLS.ClientKeyPath = c.TlsKeyPath
		}
		if c.TlsKeyData != "" {
			clientProfile.TLS.ClientKeyData = []byte(c.TlsKeyData)
		}
		if c.TlsCaPath != "" {
			clientProfile.TLS.ServerCACertPath = c.TlsCaPath
		}
		if c.TlsCaData != "" {
			clientProfile.TLS.ServerCACertData = []byte(c.TlsCaData)
		}
		if c.TlsServerName != "" {
			clientProfile.TLS.ServerName = c.TlsServerName
		}
		if c.TlsDisableHostVerification {
			clientProfile.TLS.DisableHostVerification = c.TlsDisableHostVerification
		}
	}
	// In the past, the presence of API key CLI arg did not imply TLS like it
	// does with envconfig. Therefore if there is a user-provided API key and
	// TLS is not present, explicitly disable it so API key presence doesn't
	// enable it in ToClientOptions below.
	// TODO(cretz): Or do we want to break compatibility to have TLS defaulted
	// for all API keys?
	if c.ApiKey != "" && clientProfile.TLS == nil {
		clientProfile.TLS = &envconfig.ClientConfigTLS{Disabled: true}
	}

	// If codec endpoint is set, create codec setting regardless. But if auth is
	// set, it only overrides if codec is present.
	if c.CodecEndpoint != "" {
		if clientProfile.Codec == nil {
			clientProfile.Codec = &envconfig.ClientConfigCodec{}
		}
		clientProfile.Codec.Endpoint = c.CodecEndpoint
	}
	if c.CodecAuth != "" && clientProfile.Codec != nil {
		clientProfile.Codec.Auth = c.CodecAuth
	}

	// Now load client options from the profile
	clientOptions, err := clientProfile.ToClientOptions(envconfig.ToClientOptionsOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed creating client options: %w", err)
	}
	clientOptions.Logger = log.NewStructuredLogger(cctx.Logger)
	clientOptions.Identity = clientIdentity()
	// We do not put codec on data converter here, it is applied via
	// interceptor. Same for failure conversion.
	// XXX: If this is altered to be more dynamic, have to also update
	// everywhere DataConverterWithRawValue is used.
	clientOptions.DataConverter = DataConverterWithRawValue

	// Remote codec
	if clientProfile.Codec != nil && clientProfile.Codec.Endpoint != "" {
		interceptor, err := payloadCodecInterceptor(
			clientProfile.Namespace, clientProfile.Codec.Endpoint, clientProfile.Codec.Auth)
		if err != nil {
			return nil, fmt.Errorf("failed creating payload codec interceptor: %w", err)
		}
		clientOptions.ConnectionOptions.DialOptions = append(
			clientOptions.ConnectionOptions.DialOptions, grpc.WithChainUnaryInterceptor(interceptor))
	}

	// Fixed header overrides
	clientOptions.ConnectionOptions.DialOptions = append(
		clientOptions.ConnectionOptions.DialOptions, grpc.WithChainUnaryInterceptor(fixedHeaderOverrideInterceptor))

	// Additional gRPC options
	clientOptions.ConnectionOptions.DialOptions = append(
		clientOptions.ConnectionOptions.DialOptions, cctx.Options.AdditionalClientGRPCDialOptions...)

	return client.Dial(clientOptions)
}

func fixedHeaderOverrideInterceptor(
	ctx context.Context,
	method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
) error {
	// The SDK sets some values on the outgoing metadata that we can't override
	// via normal headers, so we have to replace directly on the metadata
	md, _ := metadata.FromOutgoingContext(ctx)
	if md == nil {
		md = metadata.MD{}
	}
	md.Set("client-name", "temporal-cli")
	md.Set("client-version", Version)
	md.Set("supported-server-versions", ">=1.0.0 <2.0.0")
	md.Set("caller-type", "operator")
	ctx = metadata.NewOutgoingContext(ctx, md)
	return invoker(ctx, method, req, reply, cc, opts...)
}

func payloadCodecInterceptor(namespace, codecEndpoint, codecAuth string) (grpc.UnaryClientInterceptor, error) {
	codecEndpoint = strings.ReplaceAll(codecEndpoint, "{namespace}", namespace)

	payloadCodec := converter.NewRemotePayloadCodec(
		converter.RemotePayloadCodecOptions{
			Endpoint: codecEndpoint,
			ModifyRequest: func(req *http.Request) error {
				req.Header.Set("X-Namespace", namespace)
				if codecAuth != "" {
					req.Header.Set("Authorization", codecAuth)
				}
				return nil
			},
		},
	)
	return converter.NewPayloadCodecGRPCClientInterceptor(
		converter.PayloadCodecGRPCClientInterceptorOptions{
			Codecs: []converter.PayloadCodec{payloadCodec},
		},
	)
}

func clientIdentity() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	username := "unknown-user"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	return "temporal-cli:" + username + "@" + hostname
}

var DataConverterWithRawValue = converter.NewCompositeDataConverter(
	rawValuePayloadConverter{},
	converter.NewNilPayloadConverter(),
	converter.NewByteSlicePayloadConverter(),
	converter.NewProtoJSONPayloadConverter(),
	converter.NewProtoPayloadConverter(),
	converter.NewJSONPayloadConverter(),
)

type RawValue struct{ Payload *common.Payload }

type rawValuePayloadConverter struct{}

func (rawValuePayloadConverter) ToPayload(value any) (*common.Payload, error) {
	// Only convert if value is a raw value
	if r, ok := value.(RawValue); ok {
		return r.Payload, nil
	}
	return nil, nil
}

func (rawValuePayloadConverter) FromPayload(payload *common.Payload, valuePtr any) error {
	return fmt.Errorf("raw value unsupported from payload")
}

func (rawValuePayloadConverter) ToString(p *common.Payload) string {
	return fmt.Sprintf("<raw payload %v bytes>", len(p.Data))
}

func (rawValuePayloadConverter) Encoding() string {
	// Should never be used
	return "raw-value-encoding"
}
