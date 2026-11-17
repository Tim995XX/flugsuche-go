package main

import "testing"

func TestParseProviders(t *testing.T) {
	tests := []struct {
		in      string
		want    []string // erwartete Namen
		wantErr bool
	}{
		{in: "a=http://localhost:9001", want: []string{"a"}},
		{in: "a=http://x:1, b=https://y.example/api/", want: []string{"a", "b"}},
		{in: "", wantErr: true},
		{in: "a", wantErr: true},
		{in: "=http://x", wantErr: true},
		{in: "a=", wantErr: true},
		{in: "a=http://x,", wantErr: true},           // abschließendes Komma
		{in: "a=http://x,a=http://y", wantErr: true}, // doppelter Name
		{in: "a=localhost:9001", wantErr: true},      // fehlendes Schema
		{in: "a=ftp://x", wantErr: true},             // falsches Schema
		{in: "a=http://", wantErr: true},             // kein Host
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseProviders(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i, name := range tt.want {
				if got[i].Name() != name {
					t.Errorf("[%d].Name() = %q, want %q", i, got[i].Name(), name)
				}
			}
		})
	}
}
