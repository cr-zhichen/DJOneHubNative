package esim

import "testing"

func TestPredictCardIdentityRecognizesVerified9eSIMV3Sample(t *testing.T) {
	identity := predictCardIdentity("89044045930000000000001714825038", "37.4.3")
	if identity == nil {
		t.Fatal("predictCardIdentity() = nil")
	}
	if identity.Brand != "9eSIM" || identity.Model != "9eSIM v3" || identity.HardwareRevision != "v3.2" {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.Confidence != identityConfidenceHigh || identity.RuleID != identityRule9eSIMV3Sample {
		t.Fatalf("identity evidence = %#v", identity)
	}
	if len(identity.Evidence) != 2 || identity.Evidence[0].Name != "NekokoLPA/Vendors" || identity.Evidence[1].Name != "OpenEUICC Vendors.kt" {
		t.Fatalf("identity sources = %#v", identity.Evidence)
	}
}

func TestPredictCardIdentityRecognizesLegacyV2S(t *testing.T) {
	identity := predictCardIdentity("89044045216727494800000000000000", "36.17.4")
	if identity == nil || identity.Model != "9eSIM V2S" || identity.HardwareRevision != "v2s" {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.RuleID != identityRule9eSIMLegacy {
		t.Fatalf("rule = %q", identity.RuleID)
	}
}

func TestPredictCardIdentityTreatsKigenFamilyAsInferred(t *testing.T) {
	identity := predictCardIdentity("89044045999999999999999999999999", "37.1.41")
	if identity == nil || identity.Model != "9eSIM v3" || identity.HardwareRevision != "v3.1" {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.Confidence != identityConfidenceMedium {
		t.Fatalf("confidence = %q", identity.Confidence)
	}
}

func TestPredictCardIdentityDoesNotClaimSharedECPPrefix(t *testing.T) {
	if identity := predictCardIdentity("89086030000000000000000000000000", "37.4.3"); identity != nil {
		t.Fatalf("identity = %#v, want nil because ECP prefix is shared by multiple brands", identity)
	}
}

func TestPredictCardIdentityDoesNotApplyLegacyFirmwareFallbackToEntireKigenRange(t *testing.T) {
	if identity := predictCardIdentity("89044045999999999999999999999999", "25.1.0"); identity != nil {
		t.Fatalf("identity = %#v, want nil for an unverified Kigen card", identity)
	}
	identity := predictCardIdentity("89044045846727494800000000000000", "25.1.0")
	if identity == nil || identity.Model != "9eSIM" || identity.Confidence != identityConfidenceHigh {
		t.Fatalf("legacy identity = %#v", identity)
	}
}

func TestPrivateCardIdentityIsConfirmed(t *testing.T) {
	identity := privateCardIdentity("eSTK.me Max")
	if identity == nil || identity.Brand != "eSTK.me" || identity.Confidence != identityConfidenceConfirmed {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestCloneCardIdentityCopiesEvidence(t *testing.T) {
	original := predictCardIdentity("89044045930000000000001714825038", "37.4.3")
	cloned := cloneCardIdentity(original)
	cloned.Evidence[0].Name = "mutated"
	if original.Evidence[0].Name == "mutated" {
		t.Fatal("clone shared evidence backing storage")
	}
}
