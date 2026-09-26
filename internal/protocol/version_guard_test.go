package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/lmorchard/wideboi/internal/protocol/wirepb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
)

// wireSchemaHashes maps each protocol.Version to the sha256 hex digest of its
// deterministically marshaled FileDescriptorProto.
//
// When changing internal/protocol/wirepb/wideboi.proto:
// 1. Regenerate proto bindings (`make proto` or `buf generate`).
// 2. Bump protocol.Version in internal/protocol/version.go.
// 3. Update web/src/version.ts to match.
// 4. Record the new Version and sha256 below.
var wireSchemaHashes = map[uint32]string{
	14: "cc285ebd8a3ea0a5673d106f86a2879695035b9035c18c5f862c06c923d96754",
	15: "fee133898f4e878d0dbe8debd00d7f7804ebe95e5a36741fa0189cfd1cc4a17f",
}

func currentWireSchemaHash() (string, error) {
	descProto := protodesc.ToFileDescriptorProto(wirepb.File_internal_protocol_wirepb_wideboi_proto)
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(descProto)
	if err != nil {
		return "", fmt.Errorf("marshal wire descriptor: %w", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

func TestWireSchemaMatchesProtocolVersion(t *testing.T) {
	hash, err := currentWireSchemaHash()
	if err != nil {
		t.Fatalf("failed to compute wire schema hash: %v", err)
	}

	expected, ok := wireSchemaHashes[Version]
	if !ok {
		t.Fatalf("no schema hash recorded for protocol.Version %d; did you bump Version? Add %q to wireSchemaHashes", Version, hash)
	}

	if hash != expected {
		t.Fatalf("wire schema changed: bump protocol.Version and update this golden (current schema sha256: %s, expected for v%d: %s)", hash, Version, expected)
	}
}

func TestWebClientVersionMatchesGoProtocolVersion(t *testing.T) {
	// Look up web/src/version.ts relative to repo root.
	versionPath := filepath.Join("..", "..", "web", "src", "version.ts")
	content, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("reading web/src/version.ts: %v", err)
	}

	// Verify PROTOCOL_VERSION number
	reNum := regexp.MustCompile(`export const PROTOCOL_VERSION\s*=\s*(\d+);`)
	matchNum := reNum.FindSubmatch(content)
	if matchNum == nil {
		t.Fatalf("could not find PROTOCOL_VERSION export in %s", versionPath)
	}
	tsVer, err := strconv.ParseUint(string(matchNum[1]), 10, 32)
	if err != nil {
		t.Fatalf("parsing PROTOCOL_VERSION: %v", err)
	}
	if uint32(tsVer) != Version {
		t.Fatalf("web client PROTOCOL_VERSION (%d) does not match Go protocol.Version (%d)", tsVer, Version)
	}

	// Verify literal or expression produces wideboi.v<Version>
	wantSubproto := fmt.Sprintf("wideboi.v%d", Version)
	reLit := regexp.MustCompile(`wideboi\.v(\d+)`)
	litMatch := reLit.FindSubmatch(content)
	if litMatch != nil {
		litVer, _ := strconv.ParseUint(string(litMatch[1]), 10, 32)
		if uint32(litVer) != Version {
			t.Fatalf("web client VERSION_PROTOCOL literal (%q) does not match Go protocol.Version (%d)", string(litMatch[0]), Version)
		}
	} else {
		// If constructed dynamically as `wideboi.v${PROTOCOL_VERSION}`, check that pattern
		if !regexp.MustCompile("wideboi\\.v\\$\\{PROTOCOL_VERSION\\}").Match(content) &&
			!regexp.MustCompile(regexp.QuoteMeta(wantSubproto)).Match(content) {
			t.Fatalf("web client version.ts does not export %s", wantSubproto)
		}
	}
}
