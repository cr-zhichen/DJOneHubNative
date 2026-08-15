package main

import "sort"

type commandSchema struct {
	Command      string            `json:"command"`
	Description  string            `json:"description"`
	Scope        string            `json:"required_scope,omitempty"`
	Mutating     bool              `json:"mutating,omitempty"`
	Confirmation bool              `json:"confirmation_required,omitempty"`
	Arguments    map[string]string `json:"arguments,omitempty"`
}

var commandSchemas = []commandSchema{
	{Command: "version", Description: "Show the installed CLI version."},
	{Command: "schema [command]", Description: "Return the machine-readable command schema."},
	{Command: "capabilities", Description: "Return server-enforced capabilities and granted scopes."},
	{Command: "doctor", Description: "Inspect the CLI, access file, socket, and backend health."},
	{Command: "permissions show", Description: "Show whether AI access is enabled and list granted scopes."},
	{Command: "device status", Description: "Read modem and SIM status.", Scope: "device.read"},
	{Command: "network status", Description: "Read network diagnostics.", Scope: "network.read"},
	{Command: "network traffic", Description: "Read current network traffic counters.", Scope: "network.read"},
	{Command: "sms list", Description: "List SMS metadata; use sms get for exact content.", Scope: "sms.read", Arguments: map[string]string{"--limit": "1..100, default 20"}},
	{Command: "sms get", Description: "Read one SMS by stable ID.", Scope: "sms.read", Arguments: map[string]string{"--id": "required stable SMS ID"}},
	{Command: "sms send", Description: "Send one SMS.", Scope: "sms.send", Mutating: true, Confirmation: true, Arguments: map[string]string{"--to": "required phone number", "--message": "message text", "--message-file": "UTF-8 file path", "--request-id": "idempotency key", "--dry-run": "validate without sending", "--yes": "confirm execution"}},
	{Command: "call status", Description: "Read current call state.", Scope: "call.read"},
	{Command: "call history", Description: "List recent calls.", Scope: "call.read", Arguments: map[string]string{"--limit": "1..100, default 20"}},
	{Command: "call events", Description: "Emit call state changes as NDJSON.", Scope: "call.read", Arguments: map[string]string{"--follow": "continue until interrupted", "--interval": "poll interval, default 2s"}},
	{Command: "call dial", Description: "Place a voice call.", Scope: "call.dial", Mutating: true, Confirmation: true, Arguments: map[string]string{"--number": "required phone number", "--request-id": "idempotency key", "--dry-run": "validate without dialing", "--yes": "confirm execution"}},
	{Command: "call answer", Description: "Answer the current incoming call.", Scope: "call.answer", Mutating: true, Confirmation: true, Arguments: map[string]string{"--request-id": "idempotency key", "--dry-run": "validate without answering", "--yes": "confirm execution"}},
	{Command: "call hangup", Description: "Hang up the current call; remains a direct safety action.", Scope: "call.hangup", Mutating: true, Arguments: map[string]string{"--request-id": "idempotency key", "--dry-run": "validate without hanging up"}},
	{Command: "esim list", Description: "Read eSIM profiles and current operation.", Scope: "esim.read"},
	{Command: "esim operation", Description: "Read the current asynchronous eSIM operation.", Scope: "esim.read"},
	{Command: "esim switch", Description: "Switch to an existing eSIM profile.", Scope: "esim.switch", Mutating: true, Confirmation: true, Arguments: map[string]string{"--iccid": "required target ICCID", "--aid": "optional profile AID", "--request-id": "idempotency key", "--dry-run": "validate without switching", "--yes": "confirm execution"}},
	{Command: "agent guide", Description: "Return a copyable prompt for an AI agent."},
	{Command: "agent skill export", Description: "Export the trusted bundled Skill for the current agent to install using its native mechanism."},
	{Command: "agent skill install", Description: "Compatibility helper that installs the bundled Skill into a known agent directory.", Arguments: map[string]string{"--target": "auto, codex, claude, or agents"}},
	{Command: "agent skill status", Description: "Inspect bundled Skill installation state.", Arguments: map[string]string{"--target": "auto, codex, claude, or agents"}},
	{Command: "agent skill uninstall", Description: "Remove only Skill copies managed by DJOneHub.", Arguments: map[string]string{"--target": "auto, codex, claude, or agents"}},
}

func schemaData(filter string) (any, *commandError) {
	if filter == "" {
		commands := append([]commandSchema(nil), commandSchemas...)
		sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
		return map[string]any{
			"schema_version": "1",
			"cli_version":    version,
			"default_format": "json",
			"warning_codes": map[string]string{
				"cli_sync_required": "The installed CLI differs from the current app; sync it in DJOneHub’s AI 与 CLI page.",
			},
			"commands": commands,
		}, nil
	}
	for _, schema := range commandSchemas {
		if schema.Command == filter || commandBase(schema.Command) == filter {
			return schema, nil
		}
	}
	return nil, usageError("unknown schema command: " + filter)
}

func commandBase(command string) string {
	for index, character := range command {
		if character == ' ' || character == '[' {
			return command[:index]
		}
	}
	return command
}
