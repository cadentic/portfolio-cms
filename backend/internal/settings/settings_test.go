package settings

import (
	"strings"
	"testing"
)

func TestSiteValidate(t *testing.T) {
	si := Site{BrandSuffix: "  Sayantan   Chakraborty,  Oracle DBA "}
	if err := si.Validate(); err != nil || si.BrandSuffix != DefaultBrandSuffix {
		t.Fatalf("want normalised default, got %q err=%v", si.BrandSuffix, err)
	}
	empty := Site{}
	if err := empty.Validate(); err != nil {
		t.Fatalf("empty suffix turns branding off and must be valid: %v", err)
	}
	for _, bad := range []string{"<script>", `a"b`, strings.Repeat("x", 81)} {
		s := Site{BrandSuffix: bad}
		if s.Validate() == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}
