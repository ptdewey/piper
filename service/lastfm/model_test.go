package lastfm

import (
	"encoding/json"
	"testing"
)

func TestTracksUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string // track names, in order
		wantErr bool
	}{
		{
			name:  "array of tracks",
			input: `[{"name":"A","artist":{"#text":"x"}},{"name":"B","artist":{"#text":"y"}}]`,
			want:  []string{"A", "B"},
		},
		{
			name:  "single track as object",
			input: `{"name":"Solo","artist":{"#text":"x"}}`,
			want:  []string{"Solo"},
		},
		{
			name:  "empty array",
			input: `[]`,
			want:  []string{},
		},
		{
			name:  "null",
			input: `null`,
			want:  []string{},
		},
		{
			name:  "whitespace-padded object",
			input: "  \n\t" + `{"name":"Pad"}`,
			want:  []string{"Pad"},
		},
		{
			name:    "malformed",
			input:   `{"name":`,
			wantErr: true,
		},
		{
			name:    "wrong type",
			input:   `"a string"`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got Tracks
			err := json.Unmarshal([]byte(tc.input), &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unmarshal err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tc.want), got)
			}
			for i, name := range tc.want {
				if got[i].Name != name {
					t.Errorf("track[%d].Name = %q, want %q", i, got[i].Name, name)
				}
			}
		})
	}
}

func TestRecentTracksResponseUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNames []string
		wantUser  string
	}{
		{
			name: "multiple tracks",
			input: `{"recenttracks":{"track":[
				{"name":"A","artist":{"#text":"x"}},
				{"name":"B","artist":{"#text":"y"}}
			],"@attr":{"user":"u","total":"2","page":"1","perPage":"50","totalPages":"1"}}}`,
			wantNames: []string{"A", "B"},
			wantUser:  "u",
		},
		{
			name: "single track returned as object",
			input: `{"recenttracks":{"track":
				{"name":"Solo","artist":{"#text":"x"}}
			,"@attr":{"user":"u","total":"1","page":"1","perPage":"50","totalPages":"1"}}}`,
			wantNames: []string{"Solo"},
			wantUser:  "u",
		},
		{
			name:      "no tracks (empty array)",
			input:     `{"recenttracks":{"track":[],"@attr":{"user":"u","total":"0","page":"1","perPage":"50","totalPages":"1"}}}`,
			wantNames: []string{},
			wantUser:  "u",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var resp RecentTracksResponse
			if err := json.Unmarshal([]byte(tc.input), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := resp.RecentTracks.Attr.User; got != tc.wantUser {
				t.Errorf("user = %q, want %q", got, tc.wantUser)
			}
			if len(resp.RecentTracks.Tracks) != len(tc.wantNames) {
				t.Fatalf("len = %d, want %d", len(resp.RecentTracks.Tracks), len(tc.wantNames))
			}
			for i, name := range tc.wantNames {
				if resp.RecentTracks.Tracks[i].Name != name {
					t.Errorf("track[%d].Name = %q, want %q", i, resp.RecentTracks.Tracks[i].Name, name)
				}
			}
		})
	}
}

func TestTrackDateUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantUTS int64
		wantErr bool
	}{
		{name: "valid uts", input: `{"uts":"1700000000","#text":"whatever"}`, wantUTS: 1700000000},
		{name: "non-numeric uts", input: `{"uts":"abc","#text":""}`, wantErr: true},
		{name: "malformed", input: `{`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d TrackDate
			err := json.Unmarshal([]byte(tc.input), &d)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got := d.Unix(); got != tc.wantUTS {
				t.Errorf("uts = %d, want %d", got, tc.wantUTS)
			}
		})
	}
}
