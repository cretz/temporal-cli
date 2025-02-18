package temporalcli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/temporalio/cli/temporalcli/internal/printer"
	"go.temporal.io/sdk/contrib/envconfig"
)

func (c *TemporalConfigDeleteCommand) run(cctx *CommandContext, args []string) error {
	// Load config
	profileName := envConfigProfileName(cctx)
	conf, confProfile, err := loadEnvConfigProfile(cctx, profileName, true)
	if err != nil {
		return err
	}
	// If it's a specific prop, unset it, otherwise just remove the profile
	if c.Prop == "" {
		delete(conf.Profiles, profileName)
	} else if strings.HasPrefix(c.Prop, "grpc_meta.") {
		key := strings.TrimPrefix(c.Prop, "grpc_meta.")
		if confProfile.GRPCMeta[key] == "" {
			return fmt.Errorf("gRPC meta key %q not found", key)
		}
		delete(confProfile.GRPCMeta, key)
	} else {
		reflectVal, err := reflectEnvConfigProp(confProfile, c.Prop, true)
		if err != nil {
			return err
		}
		reflectVal.SetZero()
	}

	// Save
	return writeEnvConfigFile(cctx, conf)
}

func (c *TemporalConfigGetCommand) run(cctx *CommandContext, args []string) error {
	// Load config profile
	profileName := envConfigProfileName(cctx)
	conf, confProfile, err := loadEnvConfigProfile(cctx, profileName, true)
	if err != nil {
		return err
	}
	type prop struct {
		Property string `json:"property"`
		Value    any    `json:"value"`
	}
	// If there is a specific key requested, show it, otherwise show all
	if c.Prop != "" {
		// Single value goes into property-value structure
		reflectVal, err := reflectEnvConfigProp(confProfile, c.Prop, false)
		if err != nil {
			return err
		}
		return cctx.Printer.PrintStructured(
			prop{Property: c.Prop, Value: reflectVal.Interface()},
			printer.StructuredOptions{Table: &printer.TableOptions{}},
		)
	} else if cctx.JSONOutput {
		// If it is JSON, we want to dump the TOML structure in JSON form
		var tomlConf struct {
			Profiles map[string]any `toml:"profile"`
		}
		if b, err := conf.ToTOML(); err != nil {
			return fmt.Errorf("failed converting to TOML: %w", err)
		} else if err := toml.Unmarshal(b, &tomlConf); err != nil {
			return fmt.Errorf("failed converting from TOML: %w", err)
		}
		return cctx.Printer.PrintStructured(tomlConf.Profiles[profileName], printer.StructuredOptions{})
	} else {
		// Get every property individually as a property-value pair except "tls"
		// and zero vals
		var props []prop
		for k := range envConfigPropsToFieldNames {
			if k == "tls" {
				continue
			}
			if val, err := reflectEnvConfigProp(confProfile, k, false); err != nil {
				return err
			} else if !val.IsZero() {
				props = append(props, prop{Property: k, Value: val.Interface()})
			}
		}
		// Sort and display
		sort.Slice(props, func(i, j int) bool { return props[i].Property < props[j].Property })
		return cctx.Printer.PrintStructured(props, printer.StructuredOptions{Table: &printer.TableOptions{}})
	}
}

func (c *TemporalConfigListCommand) run(cctx *CommandContext, args []string) error {
	clientConfig, err := envconfig.LoadClientConfig(envconfig.LoadClientConfigOptions{
		ConfigFilePath: cctx.RootCommand.ConfigFile,
		EnvLookup:      cctx.Options.EnvLookup,
	})
	if err != nil {
		return err
	}
	type profile struct {
		Name string `json:"name"`
	}
	profiles := make([]profile, 0, len(clientConfig.Profiles))
	for k := range clientConfig.Profiles {
		profiles = append(profiles, profile{Name: k})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return cctx.Printer.PrintStructured(profiles, printer.StructuredOptions{Table: &printer.TableOptions{}})
}

func (c *TemporalConfigSetCommand) run(cctx *CommandContext, args []string) error {
	// Load config
	conf, confProfile, err := loadEnvConfigProfile(cctx, envConfigProfileName(cctx), true)
	if err != nil {
		return err
	}
	// As a special case, "grpc_meta." values are handled specifically
	if strings.HasPrefix(c.Prop, "grpc_meta.") {
		if confProfile.GRPCMeta == nil {
			confProfile.GRPCMeta = map[string]string{}
		}
		confProfile.GRPCMeta[strings.TrimPrefix(c.Prop, "grpc_meta.")] = c.Value
	} else {
		// Get reflect value
		reflectVal, err := reflectEnvConfigProp(confProfile, c.Prop, true)
		if err != nil {
			return err
		}
		// Set it from string
		switch reflectVal.Kind() {
		case reflect.String:
			reflectVal.SetString(c.Value)
		case reflect.Pointer:
			// Used for "tls", true makes an empty object, false sets nil
			switch c.Value {
			case "true":
				reflectVal.Set(reflect.New(reflectVal.Type().Elem()))
			case "false":
				reflectVal.SetZero()
			default:
				return fmt.Errorf("must be 'true' or 'false' to set this property")
			}
		case reflect.Slice:
			if reflectVal.Type().Elem().Kind() != reflect.Uint8 {
				return fmt.Errorf("unexpected slice of type %v", reflectVal.Type())
			}
			reflectVal.SetBytes([]byte(c.Value))
		case reflect.Bool:
			if c.Value != "true" && c.Value != "false" {
				return fmt.Errorf("must be 'true' or 'false' to set this property")
			}
			reflectVal.SetBool(c.Value == "true")
		case reflect.Map:
			return fmt.Errorf("must set each individual value of a map")
		default:
			return fmt.Errorf("unexpected type %v", reflectVal.Type())
		}
	}

	// Save
	return writeEnvConfigFile(cctx, conf)
}

func envConfigProfileName(cctx *CommandContext) string {
	if cctx.RootCommand.Profile != "" {
		return cctx.RootCommand.Profile
	} else if p := cctx.Options.EnvLookup.Getenv("TEMPORAL_PROFILE"); p != "" {
		return p
	}
	return envconfig.DefaultConfigFileProfile
}

func loadEnvConfigProfile(
	cctx *CommandContext,
	profile string,
	failIfNotFound bool,
) (*envconfig.ClientConfig, *envconfig.ClientConfigProfile, error) {
	clientConfig, err := envconfig.LoadClientConfig(envconfig.LoadClientConfigOptions{
		ConfigFilePath: cctx.RootCommand.ConfigFile,
		EnvLookup:      cctx.Options.EnvLookup,
	})
	if err != nil {
		return nil, nil, err
	}

	// Load profile
	clientProfile := clientConfig.Profiles[profile]
	if clientProfile == nil {
		if failIfNotFound {
			return nil, nil, fmt.Errorf("profile %q not found", profile)
		}
		clientProfile = &envconfig.ClientConfigProfile{}
		clientConfig.Profiles[profile] = clientProfile
	}
	return &clientConfig, clientProfile, nil
}

var envConfigPropsToFieldNames = map[string]string{
	"address":                       "Address",
	"namespace":                     "Namespace",
	"api_key":                       "APIKey",
	"tls":                           "TLS",
	"tls.disabled":                  "Disabled",
	"tls.client_cert_path":          "ClientCertPath",
	"tls.client_cert_data":          "ClientCertData",
	"tls.client_key_path":           "ClientKeyPath",
	"tls.client_key_data":           "ClientKeyData",
	"tls.server_ca_cert_path":       "ServerCACertPath",
	"tls.server_ca_cert_data":       "ServerCACertData",
	"tls.server_name":               "ServerName",
	"tls.disable_host_verification": "DisableHostVerification",
	"codec.endpoint":                "Endpoint",
	"codec.auth":                    "Auth",
	"grpc_meta":                     "GRPCMeta",
}

func reflectEnvConfigProp(
	prof *envconfig.ClientConfigProfile,
	prop string,
	failIfParentNotFound bool,
) (reflect.Value, error) {
	// Get field name
	field := envConfigPropsToFieldNames[prop]
	if field == "" {
		return reflect.Value{}, fmt.Errorf("unknown prop %q", prop)
	}

	// Load reflect val
	parentVal := reflect.ValueOf(prof)
	if strings.HasPrefix(prop, "tls.") {
		if prof.TLS == nil {
			if failIfParentNotFound {
				return reflect.Value{}, fmt.Errorf("no TLS options found")
			}
			prof.TLS = &envconfig.ClientConfigTLS{}
		}
		parentVal = reflect.ValueOf(prof.TLS)
	} else if strings.HasPrefix(prop, "codec.") {
		if prof.Codec == nil {
			if failIfParentNotFound {
				return reflect.Value{}, fmt.Errorf("no codec options found")
			}
			prof.Codec = &envconfig.ClientConfigCodec{}
		}
		parentVal = reflect.ValueOf(prof.Codec)
	}

	// Return reflected field
	return parentVal.FieldByName(field), nil
}

func writeEnvConfigFile(cctx *CommandContext, conf *envconfig.ClientConfig) error {
	// Get file
	configFile := cctx.RootCommand.ConfigFile
	if configFile == "" {
		var err error
		if configFile, err = envconfig.DefaultConfigFilePath(); err != nil {
			return err
		}
	}

	// Convert to TOML
	b, err := conf.ToTOML()
	if err != nil {
		return fmt.Errorf("failed building TOML: %w", err)
	}

	// Write to file, making dirs as needed
	cctx.Logger.Info("Writing config file", "file", configFile)
	if err := os.MkdirAll(filepath.Dir(configFile), 0700); err != nil {
		return fmt.Errorf("failed making config file parent dirs: %w", err)
	} else if err := os.WriteFile(configFile, b, 0600); err != nil {
		return fmt.Errorf("failed writing config file: %w", err)
	}
	return nil
}
