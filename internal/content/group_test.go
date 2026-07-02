package content

import "testing"

func TestGroupHashOrderIndependent(t *testing.T) {
	if GroupHash([]Hash{"sha256:a", "sha256:b"}) != GroupHash([]Hash{"sha256:b", "sha256:a"}) {
		t.Fatal("group hash must be order-independent")
	}
}

func TestGroupHashMemberChange(t *testing.T) {
	if GroupHash([]Hash{"sha256:a", "sha256:b"}) == GroupHash([]Hash{"sha256:a", "sha256:c"}) {
		t.Fatal("a member change must change the group hash")
	}
}
