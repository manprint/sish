package templates

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// extractTextFilter pulls the textFilter function out of console.tmpl so the
// real page code, not a copy, is what the test runs.
func extractTextFilter(t *testing.T) string {
	t.Helper()

	src, err := os.ReadFile("console.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	start := strings.Index(string(src), "function textFilter(")
	end := strings.Index(string(src), "function matches(")
	if start < 0 || end < start {
		t.Fatal("textFilter not found in console.tmpl")
	}

	return string(src)[start:end]
}

func TestConsoleTextFilter(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}

	cases := []struct {
		query string
		text  string
		want  bool // ignored when empty is true
		empty bool
	}{
		{query: "", empty: true},
		{query: "   ", empty: true},
		{query: "-", empty: true},
		{query: `""`, empty: true},
		{query: "api", text: "GET /API/users 200 1.2.3.4", want: true},
		{query: "api users", text: "GET /api/users 200 1.2.3.4", want: true},
		{query: "api orders", text: "GET /api/users 200 1.2.3.4", want: false},
		{query: "-health", text: "GET /api/users 200 1.2.3.4", want: true},
		{query: "-users", text: "GET /api/users 200 1.2.3.4", want: false},
		{query: "api -users", text: "GET /api/users 200 1.2.3.4", want: false},
		{query: "api -orders", text: "GET /api/users 200 1.2.3.4", want: true},
		{query: `"get /api"`, text: "GET /api/users 200 1.2.3.4", want: true},
		{query: `"api /get"`, text: "GET /api/users 200 1.2.3.4", want: false},
		{query: `-"200 1.2"`, text: "GET /api/users 200 1.2.3.4", want: false},
		{query: `"api/us`, text: "GET /api/users 200 1.2.3.4", want: true},
		{query: `"api/us`, text: "GET /api/orders 200 1.2.3.4", want: false},
	}

	script := extractTextFilter(t) + `
const cases = JSON.parse(process.argv[1]);
console.log(JSON.stringify(cases.map((c) => {
	const f = textFilter(c.query);
	return f === null ? null : f(c.text);
})));`

	type input struct {
		Query string `json:"query"`
		Text  string `json:"text"`
	}

	inputs := make([]input, len(cases))
	for i, c := range cases {
		inputs[i] = input{c.query, c.text}
	}

	payload, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(node, "-e", script, string(payload)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}

	var got []*bool
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}

	for i, c := range cases {
		switch {
		case c.empty && got[i] != nil:
			t.Errorf("query %q: want nil filter, got %v", c.query, *got[i])
		case !c.empty && got[i] == nil:
			t.Errorf("query %q: want a filter, got nil", c.query)
		case !c.empty && *got[i] != c.want:
			t.Errorf("query %q on %q: got %v, want %v", c.query, c.text, *got[i], c.want)
		}
	}
}
