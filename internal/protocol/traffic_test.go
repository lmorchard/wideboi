package protocol

import (
	"testing"
	"time"
)

func TestTimingStatAddAndMerge(t *testing.T) {
	var a TimingStat
	a.Add(3 * time.Microsecond)
	a.Add(time.Microsecond)
	if a != (TimingStat{Count: 2, TotalNanos: 4000, MaxNanos: 3000}) {
		t.Fatalf("Add: %+v", a)
	}
	a.Merge(TimingStat{Count: 1, TotalNanos: 5000, MaxNanos: 5000})
	if a != (TimingStat{Count: 3, TotalNanos: 9000, MaxNanos: 5000}) {
		t.Fatalf("Merge: %+v", a)
	}
}

// Merge sums counters for the departed aggregate; identity fields
// belong to one client and stay as they were.
func TestClientTrafficMergeSumsCounters(t *testing.T) {
	sum := ClientTraffic{ClientID: 1, Transport: "socket", ConnectedMillis: 9}
	o := ClientTraffic{ClientID: 2, Transport: "websocket", ConnectedMillis: 5,
		FullUpdates: 1, RowPatches: 2, ShiftPatches: 3, ChangedRows: 4,
		ResyncRequests: 5, SendFailures: 6, Messages: 7, PayloadBytes: 8,
		PanePayloadBytes: 9, WireBytes: 10, Encode: TimingStat{Count: 1, TotalNanos: 2, MaxNanos: 2}}
	sum.Merge(o)
	sum.Merge(o)
	want := ClientTraffic{ClientID: 1, Transport: "socket", ConnectedMillis: 9,
		FullUpdates: 2, RowPatches: 4, ShiftPatches: 6, ChangedRows: 8,
		ResyncRequests: 10, SendFailures: 12, Messages: 14, PayloadBytes: 16,
		PanePayloadBytes: 18, WireBytes: 20, Encode: TimingStat{Count: 2, TotalNanos: 4, MaxNanos: 2}}
	if sum != want {
		t.Fatalf("Merge:\n got %+v\nwant %+v", sum, want)
	}
}
