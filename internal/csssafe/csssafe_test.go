package csssafe

import "testing"

func TestColor(t *testing.T) {
	for _, c := range []string{
		"#c00", "#cc0000", "#cc0000ff", "rgb(255,0,0)",
		"rgba(255, 0, 0, .5)", "rgb(0,0,0,.5)", "rgb(1,2,3,4)", "rgba(0,0,0)",
		"rgb(100%, 0%, 50%)", "rgba(0,0,0,100%)",
		"rgb(255 0 0)", "rgba(100% 0 50%)", "rgb(100% 0 50% / .5)",
		"rgba(+255 -1 2.5e2 / 101%)", "rgb(0 0 0/.5)",
		"rgb(0\n0\t0 / 50%)",
		"rgb(256,0,0)", "rgba(0,0,0,1.01)", "rgb(-1,300,1e2)",
		"rgb(0%,101%,-1%)", "red", "KhAkI", "transparent",
	} {
		if got := Color(c); got != c {
			t.Fatalf("Color(%q) = %q", c, got)
		}
	}
	for _, c := range []string{
		"red;background:url(javascript:1)", "#c00;body{}", "url(x)", "expression(x)", "нечто",
		"rgb(,)", "rgba(1)", "rgb(1,2)", "rgb(1,2,3,4,5)", "rgba(1,2)",
		"rgb(100%,0,50%)", "rgb(1 %,0,0)", "rgb(1.,0,0)",
		"rgb(0 0)", "rgb(0 0 0 .5)", "rgb(0 0 0 /)", "rgb(0 0 0 / .5 .6)",
		"rgb(0 0 0 // .5)", "rgb(0,0,0 / .5)", "rgb(1..2,0,0)",
		"rgb(calc(0) 0 0)", "rgb(0/**/ 0 0)", "rgb(0; color:red 0 0)",
		"rgb(0\v0\v0)", "rgb(0\u00a00\u00a00)",
		"rgb(1e,0,0)", "rgb(1,2 3)", "rgb(1,2,3,)", "rgb(var(--x) 0 0)",
		"currentcolor", "Khaki",
	} {
		if got := Color(c); got != "" {
			t.Fatalf("Color(%q) = %q, want empty", c, got)
		}
	}
}

func TestColor_StandardNamedColors(t *testing.T) {
	// CSS Color 4 содержит 148 именованных цветов; transparent — отдельное
	// ключевое слово цвета, которое также входит в разрешённый контракт helper.
	if got, want := len(namedColors), 149; got != want {
		t.Fatalf("namedColors содержит %d значений, want %d", got, want)
	}
	for _, c := range []string{"blueviolet", "burlywood", "cadetblue", "chartreuse"} {
		if got := Color(c); got != c {
			t.Errorf("Color(%q) = %q", c, got)
		}
	}
}

func TestLength(t *testing.T) {
	for _, v := range []string{"0", "10px", "12.5pt", "100%", "auto"} {
		if got := Length(v); got != v {
			t.Fatalf("Length(%q) = %q", v, got)
		}
	}
	for _, v := range []string{`10px;color:red`, `url(x)`, `calc(100%)`, `auto;background:red`} {
		if got := Length(v); got != "" {
			t.Fatalf("Length(%q) = %q, want empty", v, got)
		}
	}
}

func TestFontFamily(t *testing.T) {
	if got := FontFamily(`Arial";color:red`); got != "Arialcolor:red" {
		t.Fatalf("FontFamily stripped to %q", got)
	}
}

func TestTextAlign(t *testing.T) {
	if got := TextAlign(" Center "); got != "center" {
		t.Fatalf("TextAlign = %q", got)
	}
	if got := TextAlign("left;position:absolute"); got != "" {
		t.Fatalf("TextAlign injected = %q", got)
	}
}
