package callnumber

import (
	"strings"
	"testing"
)

func TestParseSeparatesVoiceDestinationFromPostDialKeys(t *testing.T) {
	plan, err := Parse(" +8613800138000,,123,*# ")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Number != "+8613800138000" || plan.PostDial != ",,123,*#" || plan.Original != "+8613800138000,,123,*#" {
		t.Fatalf("unexpected dial plan: %+v", plan)
	}
	for _, value := range []string{"10086", "*123#", "+123"} {
		plan, err := Parse(value)
		if err != nil || plan.Number != value || plan.PostDial != "" {
			t.Fatalf("ordinary number %q changed: %+v, %v", value, plan, err)
		}
	}
}

func TestParseRejectsCommandsAndUnboundedPostDialInput(t *testing.T) {
	for _, value := range []string{
		"", "+", ",123", "123,", "123,,,", "123,+4", "12+3", "123,４", "123,1;ATH", "123,1\r\nATH",
		strings.Repeat("1", 33), "123," + strings.Repeat("1", 64),
	} {
		if _, err := Parse(value); err == nil {
			t.Errorf("accepted unsafe dial string %q", value)
		}
	}
}
