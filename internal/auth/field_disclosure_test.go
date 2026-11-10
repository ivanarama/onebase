package auth

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFieldPolicyDisclosureRoundTrip(t *testing.T) {
	var role Role
	if err := yaml.Unmarshal([]byte(`name: Chief
permissions:
  documents:
    Bid: [read, disclose]
  field_access:
    documents:
      Bid:
        Apartment: {read: hide, disclose: false}
        Phone: {read: mask_tail, keep: 4}
`), &role); err != nil {
		t.Fatal(err)
	}
	p := role.Permissions.FieldAccess.Policies("document", "Bid")
	if p["Apartment"].Disclose == nil || *p["Apartment"].Disclose || p["Phone"].Disclose != nil {
		t.Fatal("optional disclosure restriction was not parsed")
	}
	b, err := marshalPermissions(role.Permissions)
	if err != nil {
		t.Fatal(err)
	}
	restored := unmarshalPermissions([]byte(b))
	p = restored.FieldAccess.Policies("document", "Bid")
	if p["Apartment"].Disclose == nil || *p["Apartment"].Disclose || p["Phone"].Disclose != nil {
		t.Fatal("optional disclosure restriction was lost in stored permissions")
	}
}
