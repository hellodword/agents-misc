package wire

import "testing"

func TestExactFieldNamesAndObjectTypes(t *testing.T) {
	for _, data := range []string{`{"id":"first","ID":"second"}`, `null`, `{"id":null}`} {
		var v struct {
			ID string `json:"id"`
		}
		if Decode([]byte(data), &v) == nil {
			t.Fatalf("accepted ambiguous object %s", data)
		}
	}
}

func TestStrictMessages(t *testing.T) {
	for _, s := range []string{`{"a":1,"a":2}`, `{"a":{"x":1,"x":2}}`, `[1,]`, `{} {}`, `{"unknown":1}`} {
		var v struct {
			A any `json:"a"`
		}
		if Decode([]byte(s), &v) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	var v struct {
		A []map[string]int `json:"a"`
	}
	if e := Decode([]byte(`{"a":[{"x":1},{"x":2}]}`), &v); e != nil {
		t.Fatal(e)
	}
}
