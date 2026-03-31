package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestNormalizeSelectionModeAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":                     selectionModeRTT,
		"default":              selectionModeRTT,
		"rtt":                  selectionModeRTT,
		"sc":                   selectionModeSingleCircle,
		"single_circle":        selectionModeSingleCircle,
		"mc":                   selectionModeMultipleCircle,
		"multiplecircle":       selectionModeMultipleCircle,
		"rm":                   selectionModeRegionalMixnet,
		"regional_mixnet":      selectionModeRegionalMixnet,
		"lor":                  selectionModeLOR,
		"leave-one-random":     selectionModeLOR,
		"leave_one_random":     selectionModeLOR,
		"hybrid":               selectionModeHybrid,
		"random":               selectionModeRandom,
		"unknown-unrecognized": selectionModeRTT,
	}

	for input, want := range cases {
		if got := normalizeSelectionMode(input); got != want {
			t.Fatalf("normalizeSelectionMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSelectCircuitLayoutMultipleCircle(t *testing.T) {
	t.Parallel()

	sorted := testRelays(9)
	circles := buildLatencyCircles(sorted, 3)

	selected, err := selectCircuitLayout(circles, sorted, 3, 2, false)
	if err != nil {
		t.Fatalf("selectCircuitLayout() error = %v", err)
	}
	if len(selected) != 6 {
		t.Fatalf("selected len = %d, want 6", len(selected))
	}
	if selected[0].Latency > 30*time.Millisecond || selected[1].Latency > 30*time.Millisecond || selected[2].Latency > 30*time.Millisecond {
		t.Fatalf("first circuit should stay in the fastest latency circle, got %v", latenciesOf(selected[:3]))
	}
	if selected[3].Latency < 40*time.Millisecond || selected[3].Latency > 60*time.Millisecond {
		t.Fatalf("second circuit should start in the next latency circle, got %v", latenciesOf(selected[3:]))
	}
	assertUniqueRelays(t, selected)
}

func TestSelectRegionalLayoutPrefersRemoteExit(t *testing.T) {
	t.Parallel()

	sorted := testRelays(8)
	selected, err := selectRegionalLayout(sorted, 3, 2)
	if err != nil {
		t.Fatalf("selectRegionalLayout() error = %v", err)
	}
	if len(selected) != 6 {
		t.Fatalf("selected len = %d, want 6", len(selected))
	}

	exits := []RelayInfo{selected[2], selected[5]}
	for _, exit := range exits {
		if exit.Latency < 50*time.Millisecond {
			t.Fatalf("expected remote exits to come from slower half, got %v", latenciesOf(exits))
		}
	}
	assertUniqueRelays(t, selected)
}

func TestSelectRelaysUsesSamplingSizeForRTTMode(t *testing.T) {
	t.Parallel()

	rd := NewRelayDiscovery("mixnet", 3, selectionModeRTT, 0.3)
	selected, err := rd.SelectRelays(context.Background(), testRelays(6))
	if err != nil {
		t.Fatalf("SelectRelays() error = %v", err)
	}
	if len(selected) != 3 {
		t.Fatalf("selected len = %d, want 3", len(selected))
	}
	if got := latenciesOf(selected); got[0] != 10*time.Millisecond || got[1] != 20*time.Millisecond || got[2] != 30*time.Millisecond {
		t.Fatalf("RTT selection should keep the three fastest relays, got %v", got)
	}
	assertUniqueRelays(t, selected)
}

func TestSelectRelaysMultipleCircleSpreadsAcrossLatencyBands(t *testing.T) {
	t.Parallel()

	rd := NewRelayDiscovery("mixnet", 3, selectionModeMultipleCircle, 0.3)
	selected, err := rd.SelectRelays(context.Background(), testRelays(6))
	if err != nil {
		t.Fatalf("SelectRelays() error = %v", err)
	}
	if len(selected) != 3 {
		t.Fatalf("selected len = %d, want 3", len(selected))
	}
	if got := latenciesOf(selected); got[0] != 10*time.Millisecond || got[1] != 30*time.Millisecond || got[2] != 50*time.Millisecond {
		t.Fatalf("multiple-circle selection should spread picks across circles, got %v", got)
	}
	assertUniqueRelays(t, selected)
}

func testRelays(count int) []RelayInfo {
	relays := make([]RelayInfo, 0, count)
	for i := 0; i < count; i++ {
		id := peer.ID("peer-" + string(rune('a'+i)))
		relays = append(relays, RelayInfo{
			PeerID:    id,
			AddrInfo:  peer.AddrInfo{ID: id},
			Latency:   time.Duration((i+1)*10) * time.Millisecond,
			Available: true,
		})
	}
	return relays
}

func latenciesOf(relays []RelayInfo) []time.Duration {
	out := make([]time.Duration, 0, len(relays))
	for _, relay := range relays {
		out = append(out, relay.Latency)
	}
	return out
}

func assertUniqueRelays(t *testing.T, relays []RelayInfo) {
	t.Helper()

	seen := make(map[peer.ID]struct{}, len(relays))
	for _, relay := range relays {
		if _, ok := seen[relay.PeerID]; ok {
			t.Fatalf("relay %s selected more than once", relay.PeerID)
		}
		seen[relay.PeerID] = struct{}{}
	}
}
