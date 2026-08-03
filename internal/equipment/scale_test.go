package equipment

import (
	"net"
	"testing"
	"time"
)

// scaleServer — эмулятор весов: принимает запрос (ENQ) и отвечает строкой reply.
func scaleServer(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 16)
		_, _ = conn.Read(buf) // запрос веса (ENQ)
		_, _ = conn.Write([]byte(reply))
	}()
	return ln.Addr().String()
}

func TestScale_Weight(t *testing.T) {
	addr := scaleServer(t, "ST,GS,+000.250 kg\r\n")

	dev, err := Open("scale_tcp", map[string]string{"порт": addr})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer disconnect(dev)
	scale, ok := dev.(Scale)
	if !ok {
		t.Fatal("устройство не реализует Scale")
	}
	w, err := scale.Weight()
	if err != nil {
		t.Fatalf("Weight: %v", err)
	}
	if w != 0.25 {
		t.Errorf("вес = %v, ожидался 0.25", w)
	}
}

func TestParseWeight(t *testing.T) {
	cases := map[string]float64{
		"0.250":                 0.25,
		"ST,GS,+000.250 kg\r\n": 0.25,
		"1.5":                   1.5,
		"  12,340 ":             12.34,
	}
	for in, want := range cases {
		got, err := parseWeight(in)
		if err != nil {
			t.Errorf("parseWeight(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseWeight(%q) = %v, ожидалось %v", in, got, want)
		}
	}
	if _, err := parseWeight("нет числа"); err == nil {
		t.Error("ожидалась ошибка для строки без числа")
	}
}
