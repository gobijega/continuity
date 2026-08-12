package interfaces

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		"eth0":      KindEthernet,
		"eno1":      KindEthernet,
		"enp3s0":    KindEthernet,
		"ens33":     KindEthernet,
		"wlan0":     KindWiFi,
		"wlp2s0":    KindWiFi,
		"wwan0":     KindCellular,
		"rmnet0":    KindCellular,
		"wg0":       KindTunnel,
		"tun0":      KindTunnel,
		"ppp0":      KindTunnel,
		"docker0":   KindVirtual,
		"veth1a2b":  KindVirtual,
		"br-abc":    KindVirtual,
		"weirdname": KindOther,
	}
	for name, want := range cases {
		if got := classify(name); got != want {
			t.Errorf("classify(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDiscoverFindsLoopback(t *testing.T) {
	ifs, err := Discover()
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}
	if len(ifs) == 0 {
		t.Fatal("Discover() returned no interfaces")
	}
	var foundLo bool
	for _, i := range ifs {
		if i.Kind == KindLoopback {
			foundLo = true
		}
	}
	if !foundLo {
		t.Errorf("expected a loopback interface among %d discovered", len(ifs))
	}
}

func TestPrimaryIPv4(t *testing.T) {
	i := Interface{IPv4: []string{"169.254.1.1", "192.168.1.20"}}
	if got := i.PrimaryIPv4(); got != "192.168.1.20" {
		t.Errorf("PrimaryIPv4() = %q, want 192.168.1.20 (link-local should be skipped)", got)
	}
	empty := Interface{IPv4: []string{"169.254.9.9"}}
	if got := empty.PrimaryIPv4(); got != "" {
		t.Errorf("PrimaryIPv4() = %q, want empty (only link-local present)", got)
	}
}

func TestUsable(t *testing.T) {
	up := Interface{Kind: KindEthernet, Up: true, IPv4: []string{"10.0.0.5"}}
	if !up.Usable() {
		t.Error("expected an up ethernet interface with an address to be usable")
	}
	down := Interface{Kind: KindEthernet, Up: false, IPv4: []string{"10.0.0.5"}}
	if down.Usable() {
		t.Error("a down interface must not be usable")
	}
	lo := Interface{Kind: KindLoopback, Up: true, IPv4: []string{"127.0.0.1"}}
	if lo.Usable() {
		t.Error("loopback must not be usable")
	}
}
