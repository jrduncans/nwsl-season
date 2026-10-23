package app

import (
	"bytes"
	"html/template"
	"testing"
)

func TestPagePayloadRoundsFractionsAndKeepsOrder(t *testing.T) {
	value := struct {
		B      float64            `json:"b"`
		A      []any              `json:"a"`
		Nested map[string]float64 `json:"nested"`
	}{
		B:      1.6077000000000001,
		A:      []any{3, -0.00000001, 2.5, "1.23456789", nil, true, []int{}},
		Nested: map[string]float64{"x": 1e-9, "y": 123456.1234567},
	}
	got, err := payload(value).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"b":1.6077,"a":[3,0,2.5,"1.23456789",null,true,[]],"nested":{"x":0,"y":123456.123457}}`
	if string(got) != want {
		t.Errorf("payload = %s, want %s", got, want)
	}
}

func TestPagePayloadIsEscapedInTemplates(t *testing.T) {
	page := template.Must(template.New("").Funcs(template.FuncMap{"payload": payload}).Parse(`<script type="application/json">{{payload .}}</script>`))
	var out bytes.Buffer
	if err := page.Execute(&out, map[string]string{"name": "</script><b>"}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte("</script><b>")) {
		t.Errorf("payload not escaped: %s", out.String())
	}
}
