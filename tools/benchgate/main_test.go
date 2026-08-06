package main

import (
	"strings"
	"testing"
)

// Реальный вывод `benchstat -format csv`: Slow просел на 49.94% (значимо,
// p=0.002), Fast изменился незначимо («~»).
const sampleCSV = `goos: linux
goarch: amd64
pkg: example/x
,base.txt,,pr.txt,,,
,sec/op,CI,sec/op,CI,vs base,P
Fast-4,1.0025000000000001e-06,0%,1.0045e-06,0%,~,p=0.130 n=6
Slow-4,2.0025e-06,0%,3.0025000000000005e-06,0%,+49.94%,p=0.002 n=6
geomean,1.416864937105863e-06,,1.736666706653871e-06,,+22.57%,

,base.txt,,pr.txt,,,
,B/op,CI,B/op,CI,vs base,P
Fast-4,100,0%,100,0%,~,p=1.000 n=6
Slow-4,200,0%,300,0%,+50.00%,p=0.002 n=6
geomean,141.42135623730945,,141.42135623730945,,+0.00%,
`

func TestParseFindsSignificantRegression(t *testing.T) {
	regs, err := parse(strings.NewReader(sampleCSV), "sec/op", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 1 {
		t.Fatalf("просадок %d, ожидалась 1: %+v", len(regs), regs)
	}
	if regs[0].name != "Slow-4" {
		t.Errorf("имя %q, ожидалось Slow-4", regs[0].name)
	}
	if regs[0].percent < 49 || regs[0].percent > 51 {
		t.Errorf("процент %v, ожидалось ~49.94", regs[0].percent)
	}
}

// Незначимое изменение («~») гейт обязан игнорировать, иначе шум раннера
// будет ронять каждый второй PR.
func TestParseIgnoresInsignificant(t *testing.T) {
	regs, err := parse(strings.NewReader(sampleCSV), "sec/op", 0.0001)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range regs {
		if r.name == "Fast-4" {
			t.Errorf("Fast-4 помечен «~» и не должен считаться просадкой: %+v", r)
		}
	}
}

// geomean — агрегат, а не бенчмарк: он просел на 22.57%, но при пороге 20%
// ронять сборку из-за него нельзя.
func TestParseIgnoresGeomean(t *testing.T) {
	regs, err := parse(strings.NewReader(sampleCSV), "sec/op", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range regs {
		if r.name == "geomean" {
			t.Errorf("geomean не должен считаться бенчмарком: %+v", r)
		}
	}
}

// Порог выше просадки — гейт молчит.
func TestParseBelowThreshold(t *testing.T) {
	regs, err := parse(strings.NewReader(sampleCSV), "sec/op", 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 0 {
		t.Errorf("при пороге 60%% просадок быть не должно: %+v", regs)
	}
}

// Секции метрик не должны смешиваться: при metric=B/op берётся просадка
// памяти, а не времени.
func TestParseSelectsMetricSection(t *testing.T) {
	regs, err := parse(strings.NewReader(sampleCSV), "B/op", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 1 || regs[0].name != "Slow-4" {
		t.Fatalf("ожидалась одна просадка B/op у Slow-4, получено %+v", regs)
	}
	if regs[0].percent < 49 || regs[0].percent > 51 {
		t.Errorf("процент B/op = %v, ожидалось ~50", regs[0].percent)
	}
}

func TestParsePercent(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"+49.94%", 49.94, true},
		{"-12.5%", -12.5, true},
		{"~", 0, false},
		{"", 0, false},
		{"мусор", 0, false},
	} {
		got, ok := parsePercent(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parsePercent(%q) = %v,%v; ожидалось %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
