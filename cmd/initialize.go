package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cmmoran/apimodelgen/pkg/action/initialize"
	"github.com/cmmoran/apimodelgen/pkg/action/snapshot"
	"github.com/cmmoran/apimodelgen/pkg/parser"
)

func init() {
	var initializeCmd = NewInitCommand()
	var snapshotCmd = NewSnapshotCommand()
	rootCmd.AddCommand(initializeCmd)
	rootCmd.AddCommand(snapshotCmd)
}

func NewInitCommand() *cobra.Command {
	var (
		options             = &parser.Options{}
		excludeByTagStrings = make([]string, 0)
	)

	// initializeCmd represents the apimodeldto init command
	var initCmd = &cobra.Command{
		Use:   "init",
		Short: "init apis",
		Long:  "Initialize API DTOs management and versioning",
		Run: func(c *cobra.Command, args []string) {
			initialize.Generate(options)
		},
	}
	bindParserFlags(initCmd, options, &excludeByTagStrings)
	initOpts := func() {
		options.Normalize(excludeByTagStrings...)
	}
	cobra.OnInitialize(initOpts)

	return initCmd
}

// NewSnapshotCommand wires snapshot creation plus list/diff helpers against the manifest.
func NewSnapshotCommand() *cobra.Command {
	var (
		options             = &parser.Options{}
		excludeByTagStrings = make([]string, 0)
		manifestPath        = filepath.Join(".apimodelgen", "manifest.yaml")
		snapshotRoot        = filepath.Join(".apimodelgen", "snapshots")
		snapshotName        = "current"
		snapshotVersion     string
	)

	snapshotCmd := &cobra.Command{
		Use:   "snapshot",
		Short: "generate and record API snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			if snapshotVersion == "" {
				snapshotVersion = viper.GetString("version")
			}
			if snapshotVersion == "" {
				snapshotVersion = time.Now().UTC().Format("20060102T150405Z")
			}

			options.OutDir = filepath.Join(snapshotRoot, snapshotName, snapshotVersion)
			options.Normalize(excludeByTagStrings...)

			outFile, err := snapshot.Generate(options, manifestPath, snapshotName, snapshotVersion)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "snapshot %s (%s) written to %s\n", snapshotName, snapshotVersion, outFile)
			return nil
		},
	}

	bindParserFlags(snapshotCmd, options, &excludeByTagStrings)
	snapshotCmd.PersistentFlags().StringVar(&manifestPath, "manifest", manifestPath, "path to the snapshot manifest file")
	snapshotCmd.PersistentFlags().StringVar(&snapshotRoot, "snapshot-dir", snapshotRoot, "root directory for persisted snapshots")
	snapshotCmd.PersistentFlags().StringVar(&snapshotName, "snapshot-name", snapshotName, "logical name for the snapshot (e.g. release)")
	snapshotCmd.PersistentFlags().StringVar(&snapshotVersion, "snapshot-version", snapshotVersion, "version identifier to lock into the manifest")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "list snapshots recorded in the manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := snapshot.List(manifestPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "current: %s\nprevious: %s\n", manifest.CurrentVersion, manifest.PreviousVersion)
			for _, s := range manifest.Snapshots {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", s.Name, s.Version, s.File)
			}
			return nil
		},
	}

	diffCmd := &cobra.Command{
		Use:   "diff",
		Short: "diff the current snapshot against the previous one",
		RunE: func(cmd *cobra.Command, args []string) error {
			diff, err := snapshot.DiffCurrentWithPrevious(manifestPath)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), diff)
			return nil
		},
	}

	snapshotCmd.AddCommand(listCmd, diffCmd)

	return snapshotCmd
}

// bindParserFlags applies parser-related flags to a cobra command, allowing reuse across commands.
func bindParserFlags(cmd *cobra.Command, options *parser.Options, excludeByTagStrings *[]string) {
	cmd.PersistentFlags().StringVarP(&options.InDir, "input-directory", "i", "", "directory to scan")
	cmd.PersistentFlags().StringVarP(&options.OutDir, "output-directory", "o", "api", "directory to write new types")
	cmd.PersistentFlags().StringVarP(&options.OutFile, "output-file", "f", "api_gen.go", "output file where types will be written")
	cmd.PersistentFlags().StringVarP(&options.Suffix, "suffix", "s", "", "suffix to append to generated types")
	cmd.PersistentFlags().StringVar(&options.PatchSuffix, "patch-suffix", "Patch", "suffix to append to generated PATCH types")
	cmd.PersistentFlags().BoolVarP(&options.KeepORMTags, "keep-orm-tags", "k", false, "keep ORM tags in generated types")
	cmd.PersistentFlags().BoolVarP(&options.FlattenEmbedded, "flatten-embedded", "F", true, "flatten embedded types' fields into parent")
	cmd.PersistentFlags().BoolVarP(&options.IncludeEmbedded, "include-embedded", "E", false, "include embedded types with type generation")
	cmd.PersistentFlags().BoolVarP(&options.ExcludeDeprecated, "exclude-deprecated", "d", false, "exclude deprecated fields from generated types")
	cmd.PersistentFlags().StringSliceVarP(&options.ExcludeTypes, "exclude-types", "t", []string{}, "exclude named types from generated types")
	cmd.PersistentFlags().StringSliceVarP(excludeByTagStrings, "exclude-tags", "T", []string{}, "exclude fields with matching tags from generated types, ex: gorm:\",embedded\"")
}
