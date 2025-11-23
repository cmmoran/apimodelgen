package cmd

import (
	"bytes"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/viper"

	"github.com/spf13/cobra"
)

var (
	configFiles    []string
	level, version string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "",
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVarP(&level, "level", "l", "trace", "log level (debug, info, warn, error, debug+1, etc)")
	rootCmd.PersistentFlags().StringSliceVar(&configFiles, "config", []string{}, "config file(s) - multiple config files are merged with last specified file having highest priority")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	var ll slog.Level

	if err := (&ll).UnmarshalText([]byte(level)); err != nil {
		if strings.EqualFold(level, "trace") {
			ll = slog.Level(-8)
		} else {
			panic("invalid log level: " + level)
		}
	} else {
		ll = slog.LevelInfo
	}
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource:   false,
		Level:       ll,
		ReplaceAttr: nil,
	}))
	slog.SetDefault(l)

	if len(configFiles) > 0 {
		// Use config file from the flag.
		viper.SetConfigFile(configFiles[0])
	} else {
		// Find home directory.

		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc")
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		l.With("config", viper.ConfigFileUsed()).Info("using config file(s)")
	} else {
		l.With("error", err, "config", viper.ConfigFileUsed()).Info("unable to use config file(s)")
	}
	if len(configFiles) > 1 {
		for _, file := range configFiles[1:] {
			if configBytes, err := os.ReadFile(file); err == nil {
				if err = viper.MergeConfig(bytes.NewReader(configBytes)); err != nil {
					l.With("error", err, "file", file).Warn("failed to merge config file")
				} else {
					l.With("file", file).Info("merged config file")
				}
			}
		}
	}
	if len(version) > 0 {
		viper.Set("version", version)
	}

	llstr := viper.GetString("common.log.level")
	if ll == slog.Level(-8) && !strings.EqualFold("trace", llstr) && l == nil {
		if err := ll.UnmarshalText([]byte(llstr)); err != nil {
			if strings.EqualFold(llstr, "trace") {
				ll = slog.Level(-8)
			} else {
				panic("invalid log level: " + llstr)
			}
		}
		l = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource:   false,
			Level:       ll,
			ReplaceAttr: nil,
		}))

		slog.SetDefault(l)
	}
}
