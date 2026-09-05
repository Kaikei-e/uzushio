package tariff

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		line string
		want map[string]string
	}{
		{"one pair", "zone=EU", map[string]string{"zone": "EU"}},
		{
			"several pairs",
			"zone=EU;surcharge=1.25",
			map[string]string{"zone": "EU", "surcharge": "1.25"},
		},
		{
			"a value containing the separator",
			"zone=EU;note=split=allowed",
			map[string]string{"zone": "EU", "note": "split=allowed"},
		},
		{"trailing semicolon", "zone=EU;", map[string]string{"zone": "EU"}},
		{"empty segment in the middle", "zone=EU;;surcharge=0", map[string]string{"zone": "EU", "surcharge": "0"}},
		{"blank segment", "zone=EU;   ", map[string]string{"zone": "EU"}},
		{"empty value", "note=", map[string]string{"note": ""}},
		{"empty line", "", map[string]string{}},
		{"last pair wins", "zone=EU;zone=NA", map[string]string{"zone": "NA"}},
	}
	for _, c := range cases {
		got, err := Parse(c.line)
		if err != nil {
			t.Errorf("%s: Parse(%q) returned %v, want no error", c.name, c.line, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Parse(%q) = %v, want %v", c.name, c.line, got, c.want)
		}
	}
}

func TestParseRejectsASegmentWithNoSeparator(t *testing.T) {
	for _, line := range []string{"zone", "zone=EU;surcharge", "surcharge;zone=EU"} {
		got, err := Parse(line)
		if err == nil {
			t.Errorf("Parse(%q) = %v, want an error", line, got)
			continue
		}
		if got != nil {
			t.Errorf("Parse(%q) returned a map %v alongside its error", line, got)
		}
		if !strings.Contains(err.Error(), "surcharge") && !strings.Contains(err.Error(), "zone") {
			t.Errorf("Parse(%q) error %q does not name the bad segment", line, err)
		}
	}
}
