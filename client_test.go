package ratgdo

import "testing"

func TestListEntityObjectID(t *testing.T) {
	cases := []struct {
		name     string
		objectID string
		entity   string
		want     string
	}{
		{name: "uses provided object id", objectID: "door", entity: "Door", want: "door"},
		{name: "lowercases and underscores spaces", entity: "Query Status", want: "query_status"},
		{name: "preserves dashes", entity: "Side-Light", want: "side-light"},
		{name: "sanitizes punctuation", entity: "Door/Status", want: "door_status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := listEntityObjectID(tc.objectID, tc.entity); got != tc.want {
				t.Fatalf("listEntityObjectID(%q, %q) = %q, want %q", tc.objectID, tc.entity, got, tc.want)
			}
		})
	}
}
