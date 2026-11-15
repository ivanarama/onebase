package sheet

import "testing"

func TestTabularNumericFormats(t *testing.T) {
	for _, tt := range []struct{ name, value, format, want string }{
		{"decimal", "12.5", "ЧГ=0;ЧДЦ=2", "12,50"},
		{"round-positive", "1.005", "ЧГ=0;ЧДЦ=2", "1,01"},
		{"round-negative", "-1.005", "ЧГ=0;ЧДЦ=2", "-1,01"},
		{"group", "12345.5", "ЧДЦ=2", "12 345,50"},
		{"zero-empty", "0", "ЧН=;ЧДЦ=2", ""},
		{"zero-label", "0", "ЧН=—;ЧДЦ=2", "—"},
		{"separators", "1234.25", "ЧДЦ=2;ЧРД=.;ЧРГ=_", "1_234.25"},
		{"percent", "0.125", "0.0%", "12.5%"},
		{"optional", "12.5", "0.##", "12.5"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCell(tt.value)
			c.Format = tt.format
			if got := c.DisplayText(); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
			if c.Value != tt.value {
				t.Fatal("formatter changed numeric source")
			}
		})
	}
}

func TestTabularSnapshotDetached(t *testing.T) {
	d := NewDocument()
	c := d.GetOrCreateCell(0, 0)
	c.Text = "before"
	c.Drilldown = &Reference{Entity: "Own", ID: "before"}
	d.Groups = []RowGroup{{Start: 0, Level: 1, Expanded: true}}
	d.ColWidths[0] = 100
	snap := d.Snapshot()
	c.Text = "after"
	c.Drilldown.ID = "after"
	d.Groups[0].Expanded = false
	d.ColWidths[0] = 900
	if snap.GetCell(0, 0).Text != "before" || snap.GetCell(0, 0).Drilldown.ID != "before" || !snap.Groups[0].Expanded || snap.ColWidths[0] != 100 {
		t.Fatal("snapshot aliases builder")
	}
}
