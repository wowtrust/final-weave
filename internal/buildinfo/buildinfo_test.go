package buildinfo

import "testing"

func TestCurrentIncludesRuntimeMetadata(t *testing.T) {
	info := Current()
	if info.Version == "" || info.Commit == "" || info.Date == "" {
		t.Fatalf("build metadata contains an empty field: %+v", info)
	}
	if info.GoVersion == "" || info.OS == "" || info.Arch == "" {
		t.Fatalf("runtime metadata contains an empty field: %+v", info)
	}
}
