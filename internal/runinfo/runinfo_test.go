package runinfo

import "testing"

func TestParsePower(t *testing.T) {
	tests := []struct {
		name, out, power, battery string
	}{
		{"full on ac", "Now drawing from 'AC Power'\n -InternalBattery-0 (id=23003235)\t100%; charged; 0:00 remaining present: true", "ac", "100%; charged"},
		{"charging", "Now drawing from 'AC Power'\n -InternalBattery-0 (id=23003235)\t82%; charging; (no estimate) present: true", "ac", "82%; charging"},
		{"finishing", "Now drawing from 'AC Power'\n -InternalBattery-0 (id=23003235)\t99%; finishing charge; 0:06 remaining present: true", "ac", "99%; finishing charge"},
		{"held by optimized charging", "Now drawing from 'AC Power'\n -InternalBattery-0 (id=23003235)\t80%; AC attached; not charging present: true", "ac", "80%; AC attached"},
		{"on battery", "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=23003235)\t95%; discharging; 5:00 remaining present: true", "battery", "95%; discharging"},
		{"desktop", "Now drawing from 'AC Power'", "ac", "none"},
		{"pmset failed", "unknown", "unknown", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			power, battery := parsePower(tt.out)
			if power != tt.power || battery != tt.battery {
				t.Errorf("parsePower = (%q, %q), want (%q, %q)", power, battery, tt.power, tt.battery)
			}
		})
	}
}
