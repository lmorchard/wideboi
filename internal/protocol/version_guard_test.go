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
	16: "47a55b59b89199a5b1c94ab05b00aecd79374f954cc8469a011d222ee3705833",
	17: "9020759612fba15731815bdf5d3548a06e632b676db92e8b9f2319003cfa07b7",
	18: "911852df18c706d856dcce861ab0183fca237d2f33896f4d4d5651cb23289ca5",
	19: "6e9e477a34331c51543b012f3dccb85076accfcbf8d20689f4ff95f98bba1b52",
	20: "8639e1a87b92b2491b2ec4a11d849b7b0bfc1c9b633bbabcae03d40429fc87e8",
	21: "cf5be0dcf887222baafe5bb2f845ba867958c053cf0d8002dcf4ed2c7d6e2da2",
	22: "7edeaea11093746dbd09f0534992f526e72fdff6e622309a3ad130aa027086bf",
	23: "9719ea5d789049669389d82ecbf1d949ec9d885c5aaf284af9e1dd71ce7306d4",
	24: "7cba8b717b23fb9126f4174de9b56cdd1e789fb2b0ee0d738441515278755264",
	25: "fb63a2f7e4d1be825c3c02b0dc47d92e1c1091d7aeba41792e98b8175a6dff00",
	26: "277d22c5211c8f948bbee8ea703db531338e1b99b4fbfbca8a008a4424a052ec",
	27: "d02f10edf19fa8b204cb703ded0a6d51e487334a26f771f9cfe352703c54674c",
	28: "4f03942f5ed99698f4234e3c32158d6339d37f81c0ab3cb073d8ef367b21e1d3",
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
