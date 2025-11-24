package cmd

import (
	"github.com/spf13/cobra"

	"github.com/cmmoran/apimodelgen/pkg/action/initialize"
	"github.com/cmmoran/apimodelgen/pkg/parser"
)

func init() {
	var initializeCmd = NewInitCommand()
	rootCmd.AddCommand(initializeCmd)
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
	initCmd.PersistentFlags().StringVarP(&options.InDir, "input-directory", "i", "", "directory to scan")
	initCmd.PersistentFlags().StringVarP(&options.OutDir, "output-directory", "o", "api", "directory to write new types")
	initCmd.PersistentFlags().StringVarP(&options.OutFile, "output-file", "f", "api_gen.go", "output file where types will be written")
	initCmd.PersistentFlags().StringVarP(&options.Suffix, "suffix", "s", "", "suffix to append to generated types")
	initCmd.PersistentFlags().StringVar(&options.PatchSuffix, "patch-suffix", "Patch", "suffix to append to generated PATCH types")
	initCmd.PersistentFlags().BoolVarP(&options.KeepORMTags, "keep-orm-tags", "k", false, "keep ORM tags in generated types")
	initCmd.PersistentFlags().BoolVarP(&options.FlattenEmbedded, "flatten-embedded", "F", true, "flatten embedded types' fields into parent")
	initCmd.PersistentFlags().BoolVarP(&options.IncludeEmbedded, "include-embedded", "E", false, "include embedded types with type generation")
	initCmd.PersistentFlags().BoolVarP(&options.ExcludeDeprecated, "exclude-deprecated", "d", false, "exclude deprecated fields from generated types")
	initCmd.PersistentFlags().StringSliceVarP(&options.ExcludeTypes, "exclude-types", "t", []string{}, "exclude named types from generated types")
	initCmd.PersistentFlags().StringSliceVarP(&excludeByTagStrings, "exclude-tags", "T", []string{}, "exclude fields with matching tags from generated types, ex: gorm:\",embedded\"")
	initOpts := func() {
		options.Normalize(excludeByTagStrings...)
	}
	cobra.OnInitialize(initOpts)

	return initCmd
}
