package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeCNI writes an executable that prints its version the way cnivpc does for --version.
func fakeCNI(t *testing.T, path, version string) {
	t.Helper()
	script := "#!/bin/sh\necho 'ucloud-uk8s-cnivpc version " + version + "' >&2\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestChecksame(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	fakeCNI(t, src, "1.0.4-lts-sts2")

	if same, err := checksame(src, dst); err != nil || same {
		t.Fatalf("missing target: same=%v err=%v, want false nil", same, err)
	}
	fakeCNI(t, dst, "1.0.4-lts")
	if same, err := checksame(src, dst); err != nil || same {
		t.Fatalf("different version: same=%v err=%v, want false nil", same, err)
	}
	fakeCNI(t, dst, "1.0.4-lts-sts2")
	if same, err := checksame(src, dst); err != nil || !same {
		t.Fatalf("same version: same=%v err=%v, want true nil", same, err)
	}
	if err := os.WriteFile(dst, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := checksame(src, dst); err == nil {
		t.Fatal("broken target: want error")
	}
}

func TestCopyFileReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	fakeCNI(t, src, "new")
	fakeCNI(t, dst, "old")
	// a stale tmp from an interrupted run must not block the copy
	if err := os.WriteFile(dst+".tmp", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(src)
	got, _ := os.ReadFile(dst)
	if string(got) != string(want) {
		t.Fatalf("dst = %q, want %q", got, want)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
		t.Fatalf("dst mode = %v, want 0755", fi.Mode().Perm())
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp left behind: %v", err)
	}
}

func TestCNIInitModeFromEnv(t *testing.T) {
	for _, c := range []struct {
		value   string
		set     bool
		want    bool
		wantErr bool
	}{
		{set: false, want: false},
		{value: "true", set: true, want: true},
		{value: " TRUE ", set: true, want: true},
		{value: "false", set: true, want: false},
		{value: "yes", set: true, wantErr: true},
	} {
		if c.set {
			t.Setenv(envCNIInitOverwrite, c.value)
		} else {
			os.Unsetenv(envCNIInitOverwrite)
		}
		got, err := cniInitModeFromEnv()
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%q set=%v: got %v err=%v", c.value, c.set, got, err)
		}
	}
}
