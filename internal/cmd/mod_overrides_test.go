package cmd

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestModOverrides(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := &cobra.Command{}
	addModOverrideFlags(cmd)
	if err := cmd.Flags().Parse([]string{"--exclude-mods", "mekalus-oculus-fork-with-fixed-mekanism-mekasuit, 238222  appleskin"}); err != nil {
		t.Fatal(err)
	}
	// Config / environment values are used when the flag is not given.
	viper.Set("include_mods", "ctm,particular-reforged")

	excludes, includes := modOverrides(cmd)
	if want := []string{"mekalus-oculus-fork-with-fixed-mekanism-mekasuit", "238222", "appleskin"}; !slices.Equal(excludes, want) {
		t.Errorf("excludes = %q, want %q", excludes, want)
	}
	if want := []string{"ctm", "particular-reforged"}; !slices.Equal(includes, want) {
		t.Errorf("includes = %q, want %q", includes, want)
	}
}
