package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// Forge's stock user_jvm_args.txt only mentions -Xmx in comments.
const forgeUserJVMArgs = "# Xmx and Xms set the maximum and minimum RAM usage, respectively.\r\n" +
	"# For example, to set the maximum to 3GB: -Xmx3G\r\n" +
	"\r\n"

func TestSetMaxHeap(t *testing.T) {
	cases := []struct {
		name, before, want string
	}{
		{
			name:   "stock file gets the flag appended",
			before: forgeUserJVMArgs,
			want:   "# Xmx and Xms set the maximum and minimum RAM usage, respectively.\r\n# For example, to set the maximum to 3GB: -Xmx3G\r\n-Xmx8G\r\n",
		},
		{
			name:   "existing flag is replaced in place",
			before: "# Uncomment the next line to set it.\n-Xmx4G\n-XX:+UseG1GC\n",
			want:   "# Uncomment the next line to set it.\n-Xmx8G\n-XX:+UseG1GC\n",
		},
		{
			name:   "flag sharing a line with others",
			before: "-Xms1G -Xmx4G -XX:+UseG1GC\n",
			want:   "-Xms1G -Xmx8G -XX:+UseG1GC\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "user_jvm_args.txt")
			if err := os.WriteFile(path, []byte(tc.before), 0o644); err != nil {
				t.Fatal(err)
			}
			written, err := SetMaxHeap(dir, "8G")
			if err != nil || !written {
				t.Fatalf("SetMaxHeap = %v, %v", written, err)
			}
			got, _ := os.ReadFile(path)
			if string(got) != tc.want {
				t.Errorf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestSetMaxHeapWithoutUserArgsFile(t *testing.T) {
	dir := t.TempDir()
	written, err := SetMaxHeap(dir, "8G")
	if err != nil || written {
		t.Fatalf("SetMaxHeap = %v, %v; want false, nil", written, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "user_jvm_args.txt")); !os.IsNotExist(err) {
		t.Error("user_jvm_args.txt should not be created")
	}
}

func TestValidHeapSize(t *testing.T) {
	for _, ok := range []string{"4G", "2048M", "512m", "8g"} {
		if !ValidHeapSize(ok) {
			t.Errorf("ValidHeapSize(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "8GB", "4.5G", "0G", "G", "-Xmx4G", "4"} {
		if ValidHeapSize(bad) {
			t.Errorf("ValidHeapSize(%q) = true", bad)
		}
	}
}
