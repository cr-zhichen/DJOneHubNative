package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteSMSSenderRemovesMemoryAndArchive(t *testing.T) {
	archive := newSMSArchive(filepath.Join(t.TempDir(), "sms-archive.json"))
	archive.items = []receivedSMS{
		{Sender: "+8613800000000", Content: "archived", Timestamp: time.Unix(10, 0), Archived: true},
		{Sender: "+8613900000000", Content: "keep archive", Timestamp: time.Unix(20, 0), Archived: true},
	}
	archive.save()

	a := &app{
		smsArchive: archive,
		sms: []receivedSMS{
			{Sender: "+8613800000000", Content: "sent", Timestamp: time.Unix(30, 0), Direction: "out"},
			{Sender: "+8613900000000", Content: "keep memory", Timestamp: time.Unix(40, 0)},
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/sms/delete-sender",
		bytes.NewBufferString(`{"sender":" +8613800000000 "}`))
	recorder := httptest.NewRecorder()
	a.deleteSMSSender(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Deleted      bool `json:"deleted"`
		DeletedCount int  `json:"deleted_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Deleted || response.DeletedCount != 2 {
		t.Fatalf("response = %+v, want deleted_count 2", response)
	}
	for _, item := range a.allSMS() {
		if smsSenderMatches(item.Sender, "+8613800000000") {
			t.Fatalf("target sender still present: %+v", item)
		}
	}
	if got := len(a.allSMS()); got != 2 {
		t.Fatalf("remaining messages = %d, want 2", got)
	}

	reloaded := newSMSArchive(archive.path)
	if got := len(reloaded.snapshot()); got != 1 {
		t.Fatalf("persisted archive messages = %d, want 1", got)
	}
}

func TestDeleteSMSSenderRejectsEmptySender(t *testing.T) {
	a := &app{}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/sms/delete-sender",
		bytes.NewBufferString(`{"sender":"  "}`))
	recorder := httptest.NewRecorder()

	a.deleteSMSSender(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestDeleteSMSRemovesOnlyClosestMatchingMessage(t *testing.T) {
	target := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	archive := newSMSArchive(filepath.Join(t.TempDir(), "sms-archive.json"))
	archive.items = []receivedSMS{
		{Sender: "+8613800000000", Content: "same content", Timestamp: target.Add(-30 * time.Second), Archived: true},
		{Sender: "+8613800000000", Content: "same content", Timestamp: target, Archived: true},
	}
	archive.save()

	a := &app{
		smsArchive: archive,
		sms: []receivedSMS{
			{Sender: "+8613800000000", Content: "same content", Timestamp: target.Add(-30 * time.Second)},
			{Sender: "+8613800000000", Content: "same content", Timestamp: target},
		},
	}
	payload, err := json.Marshal(map[string]any{
		"sender":    "+8613800000000",
		"content":   "same content",
		"timestamp": target,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sms/delete", bytes.NewReader(payload))
	recorder := httptest.NewRecorder()

	a.deleteSMS(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := len(a.sms); got != 1 {
		t.Fatalf("memory messages = %d, want 1", got)
	}
	if !a.sms[0].Timestamp.Equal(target.Add(-30 * time.Second)) {
		t.Fatalf("wrong memory message remained: %s", a.sms[0].Timestamp)
	}
	remainingArchive := archive.snapshot()
	if got := len(remainingArchive); got != 1 {
		t.Fatalf("archive messages = %d, want 1", got)
	}
	if !remainingArchive[0].Timestamp.Equal(target.Add(-30 * time.Second)) {
		t.Fatalf("wrong archive message remained: %s", remainingArchive[0].Timestamp)
	}
}

func TestDeleteSMSDoesNotDeleteNearbyMessageFromOtherStore(t *testing.T) {
	target := time.Date(2026, time.August, 9, 12, 0, 0, 500_000_000, time.UTC)
	targetItem := receivedSMS{Sender: "+8613800000000", Content: "same content", Timestamp: target}
	archive := newSMSArchive(filepath.Join(t.TempDir(), "sms-archive.json"))
	archive.items = []receivedSMS{
		{Sender: "+8613800000000", Content: "same content", Timestamp: target.Add(500 * time.Millisecond), Archived: true},
	}
	archive.save()
	a := &app{
		smsArchive: archive,
		sms:        []receivedSMS{targetItem},
	}
	payload, err := json.Marshal(map[string]any{
		"id":      smsStableID(targetItem),
		"sender":  "+8613800000000",
		"content": "same content",
		// Match the precision Swift's ISO-8601 encoder sends back to Go.
		"timestamp": target.Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sms/delete", bytes.NewReader(payload))
	recorder := httptest.NewRecorder()

	a.deleteSMS(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := len(a.sms); got != 0 {
		t.Fatalf("memory messages = %d, want 0", got)
	}
	if got := len(archive.snapshot()); got != 1 {
		t.Fatalf("nearby archive message was deleted; remaining = %d", got)
	}
}

func TestDeleteSMSByIDDoesNotDeleteNearbyMemoryMessageWhenTargetArchived(t *testing.T) {
	target := time.Date(2026, time.August, 9, 12, 0, 0, 500_000_000, time.UTC)
	targetItem := receivedSMS{
		Sender: "+8613800000000", Content: "same content", Timestamp: target, Archived: true,
	}
	archive := newSMSArchive(filepath.Join(t.TempDir(), "sms-archive.json"))
	archive.items = []receivedSMS{targetItem}
	archive.save()
	a := &app{
		smsArchive: archive,
		sms: []receivedSMS{
			{Sender: "+8613800000000", Content: "same content", Timestamp: target.Add(500 * time.Millisecond)},
		},
	}
	payload, err := json.Marshal(map[string]any{
		"id":        smsStableID(targetItem),
		"sender":    "+8613800000000",
		"content":   "same content",
		"timestamp": target.Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sms/delete", bytes.NewReader(payload))
	recorder := httptest.NewRecorder()

	a.deleteSMS(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := len(archive.snapshot()); got != 0 {
		t.Fatalf("archive messages = %d, want 0", got)
	}
	if got := len(a.sms); got != 1 {
		t.Fatalf("nearby memory message was deleted; remaining = %d", got)
	}
}

func TestAllSMSIncludesStableID(t *testing.T) {
	item := receivedSMS{
		Sender: "+8613800000000", Content: "content", Timestamp: time.Unix(100, 123),
	}
	a := &app{sms: []receivedSMS{item}}

	items := a.allSMS()

	if len(items) != 1 || items[0].ID != smsStableID(item) {
		t.Fatalf("stable id = %q, want %q", items[0].ID, smsStableID(item))
	}
}
