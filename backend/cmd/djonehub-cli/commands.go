package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

func dispatch(ctx context.Context, options globalOptions, args []string, stdout io.Writer) (commandResult, *commandError) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		data, err := schemaData("")
		return commandResult{Command: "schema", Data: data}, err
	}
	switch args[0] {
	case "version", "--version":
		if len(args) != 1 {
			return commandResult{Command: "version"}, usageError("version does not accept arguments")
		}
		return commandResult{Command: "version", Data: map[string]string{"version": version}}, nil
	case "schema":
		data, err := schemaData(strings.Join(args[1:], " "))
		return commandResult{Command: "schema", Data: data}, err
	case "capabilities":
		return capabilitiesCommand(ctx, options, args[1:])
	case "doctor":
		return doctorCommand(ctx, options, args[1:])
	case "permissions":
		return permissionsCommand(options, args[1:])
	case "device":
		return deviceCommand(ctx, options, args[1:])
	case "network":
		return networkCommand(ctx, options, args[1:])
	case "sms":
		return smsCommand(ctx, options, args[1:])
	case "call":
		return callCommand(ctx, options, args[1:], stdout)
	case "esim":
		return esimCommand(ctx, options, args[1:])
	case "agent":
		return agentCommand(options, args[1:])
	default:
		return commandResult{Command: args[0]}, usageError("unknown command: " + args[0] + "; run djonehub schema")
	}
}

func capabilitiesCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) != 0 {
		return commandResult{Command: "capabilities"}, usageError("capabilities does not accept arguments")
	}
	client, err := newAPIClient(options)
	if err != nil {
		return commandResult{Command: "capabilities"}, err
	}
	response, requestErr := client.request(ctx, http.MethodGet, "/api/agent/capabilities", nil, "")
	if requestErr != nil {
		return commandResult{Command: "capabilities"}, requestErr
	}
	return commandResult{Command: "capabilities", Data: map[string]any{
		"cli_version": version,
		"server":      response.Body,
	}}, nil
}

func doctorCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) != 0 {
		return commandResult{Command: "doctor"}, usageError("doctor does not accept arguments")
	}
	checks := make([]map[string]any, 0, 4)
	ok := true
	config, configErr := readAccessConfig(options.ConfigPath)
	configCheck := map[string]any{"name": "access_config", "path": options.ConfigPath, "ok": configErr == nil}
	if configErr != nil {
		configCheck["message"] = configErr.Error()
		ok = false
	} else {
		configCheck["enabled"] = config.Enabled
		configCheck["scope_count"] = len(config.Scopes)
		if !config.Enabled {
			ok = false
		}
	}
	checks = append(checks, configCheck)

	socketInfo, socketErr := os.Lstat(options.SocketPath)
	socketOK := socketErr == nil && socketInfo.Mode()&os.ModeSocket != 0 && socketInfo.Mode().Perm()&0o077 == 0
	socketCheck := map[string]any{"name": "unix_socket", "path": options.SocketPath, "ok": socketOK}
	if socketErr != nil {
		socketCheck["message"] = socketErr.Error()
	} else {
		socketCheck["mode"] = fmt.Sprintf("%04o", socketInfo.Mode().Perm())
		if !socketOK {
			socketCheck["message"] = "socket must exist, be a Unix socket, and be owner-only"
		}
	}
	if !socketOK {
		ok = false
	}
	checks = append(checks, socketCheck)

	backendCheck := map[string]any{"name": "backend_health", "ok": false}
	if client, clientErr := newAPIClient(options); clientErr != nil {
		backendCheck["message"] = clientErr.Message
		ok = false
	} else if response, requestErr := client.request(ctx, http.MethodGet, "/api/health", nil, ""); requestErr != nil {
		backendCheck["message"] = requestErr.Message
		backendCheck["code"] = requestErr.Code
		ok = false
	} else {
		backendCheck["ok"] = true
		backendCheck["response"] = response.Body
	}
	checks = append(checks, backendCheck)

	return commandResult{Command: "doctor", Data: map[string]any{
		"ok": ok, "cli_version": version, "checks": checks,
	}}, nil
}

func permissionsCommand(options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) != 1 || args[0] != "show" {
		return commandResult{Command: "permissions"}, usageError("usage: djonehub permissions show")
	}
	config, err := readAccessConfig(options.ConfigPath)
	if err != nil {
		return commandResult{Command: "permissions.show"}, &commandError{
			Code: "cli_access_not_configured", Message: err.Error(), ExitCode: 3,
		}
	}
	scopes := append([]string(nil), config.Scopes...)
	sort.Strings(scopes)
	return commandResult{Command: "permissions.show", Data: map[string]any{
		"enabled": config.Enabled, "scopes": scopes, "updated_at": config.UpdatedAt,
		"token_present": config.Token != "",
	}}, nil
}

func deviceCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) != 1 || args[0] != "status" {
		return commandResult{Command: "device"}, usageError("usage: djonehub device status")
	}
	return getCommand(ctx, options, "device.status", "/api/status")
}

func networkCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) != 1 {
		return commandResult{Command: "network"}, usageError("usage: djonehub network status|traffic")
	}
	switch args[0] {
	case "status":
		return getCommand(ctx, options, "network.status", "/api/network")
	case "traffic":
		return getCommand(ctx, options, "network.traffic", "/api/network/traffic")
	default:
		return commandResult{Command: "network"}, usageError("usage: djonehub network status|traffic")
	}
}

func getCommand(ctx context.Context, options globalOptions, name, path string) (commandResult, *commandError) {
	client, err := newAPIClient(options)
	if err != nil {
		return commandResult{Command: name}, err
	}
	response, requestErr := client.request(ctx, http.MethodGet, path, nil, "")
	if requestErr != nil {
		return commandResult{Command: name}, requestErr
	}
	return commandResult{Command: name, Data: response.Body}, nil
}

func smsCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) == 0 {
		return commandResult{Command: "sms"}, usageError("usage: djonehub sms list|get|send")
	}
	switch args[0] {
	case "list":
		set := newFlagSet("sms list")
		limit := set.String("limit", "20", "maximum number of messages")
		if err := parseFlagSet(set, args[1:]); err != nil {
			return commandResult{Command: "sms.list"}, err
		}
		count, countErr := parseBoundedInt(*limit, 1, 100, "--limit")
		if countErr != nil {
			return commandResult{Command: "sms.list"}, countErr
		}
		values := url.Values{"limit": {fmt.Sprint(count)}, "view": {"metadata"}}
		return getCommand(ctx, options, "sms.list", queryPath("/api/sms", values))
	case "get":
		set := newFlagSet("sms get")
		id := set.String("id", "", "stable SMS ID")
		if err := parseFlagSet(set, args[1:]); err != nil {
			return commandResult{Command: "sms.get"}, err
		}
		if strings.TrimSpace(*id) == "" {
			return commandResult{Command: "sms.get"}, usageError("--id is required")
		}
		return getCommand(ctx, options, "sms.get", queryPath("/api/sms", url.Values{"id": {*id}}))
	case "send":
		return sendSMSCommand(ctx, options, args[1:])
	default:
		return commandResult{Command: "sms"}, usageError("usage: djonehub sms list|get|send")
	}
}

func sendSMSCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	set := newFlagSet("sms send")
	to := set.String("to", "", "destination phone number")
	message := set.String("message", "", "message text")
	messageFile := set.String("message-file", "", "UTF-8 message file")
	requestID := set.String("request-id", "", "idempotency key")
	dryRun := set.Bool("dry-run", false, "validate without sending")
	yes := set.Bool("yes", false, "confirm sending")
	if err := parseFlagSet(set, args); err != nil {
		return commandResult{Command: "sms.send"}, err
	}
	phone, validationErr := validatePhone(*to)
	if validationErr != nil {
		return commandResult{Command: "sms.send"}, validationErr
	}
	text, messageErr := readMessage(*message, *messageFile)
	if messageErr != nil {
		return commandResult{Command: "sms.send"}, messageErr
	}
	id, idErr := ensureRequestID("sms", *requestID)
	if idErr != nil {
		return commandResult{Command: "sms.send"}, idErr
	}
	payload := map[string]string{"phone": phone, "message": text}
	if *dryRun {
		return dryRunResult("sms.send", "sms.send", payload, id), nil
	}
	if !*yes {
		return commandResult{Command: "sms.send", RequestID: id}, confirmationError("sending an SMS", id)
	}
	return mutateCommand(ctx, options, "sms.send", http.MethodPost, "/api/sms/send", payload, id)
}

func callCommand(ctx context.Context, options globalOptions, args []string, stdout io.Writer) (commandResult, *commandError) {
	if len(args) == 0 {
		return commandResult{Command: "call"}, usageError("usage: djonehub call status|history|events|dial|answer|hangup")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return commandResult{Command: "call.status"}, usageError("call status does not accept arguments")
		}
		return getCommand(ctx, options, "call.status", "/api/call/status")
	case "history":
		set := newFlagSet("call history")
		limit := set.String("limit", "20", "maximum number of calls")
		if err := parseFlagSet(set, args[1:]); err != nil {
			return commandResult{Command: "call.history"}, err
		}
		count, countErr := parseBoundedInt(*limit, 1, 100, "--limit")
		if countErr != nil {
			return commandResult{Command: "call.history"}, countErr
		}
		return callHistoryCommand(ctx, options, count)
	case "events":
		return callEventsCommand(ctx, options, args[1:], stdout)
	case "dial":
		return callDialCommand(ctx, options, args[1:])
	case "answer":
		return callAnswerCommand(ctx, options, args[1:])
	case "hangup":
		return callHangupCommand(ctx, options, args[1:])
	default:
		return commandResult{Command: "call"}, usageError("usage: djonehub call status|history|events|dial|answer|hangup")
	}
}

func callHistoryCommand(ctx context.Context, options globalOptions, limit int) (commandResult, *commandError) {
	client, err := newAPIClient(options)
	if err != nil {
		return commandResult{Command: "call.history"}, err
	}
	response, requestErr := client.request(ctx, http.MethodGet, "/api/calls", nil, "")
	if requestErr != nil {
		return commandResult{Command: "call.history"}, requestErr
	}
	var items []json.RawMessage
	if err := json.Unmarshal(response.Body, &items); err != nil {
		return commandResult{Command: "call.history"}, &commandError{Code: "invalid_backend_response", Message: err.Error(), ExitCode: 4}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return commandResult{Command: "call.history", Data: items}, nil
}

func callEventsCommand(ctx context.Context, options globalOptions, args []string, stdout io.Writer) (commandResult, *commandError) {
	set := newFlagSet("call events")
	follow := set.Bool("follow", false, "continue until interrupted")
	intervalText := set.String("interval", "2s", "poll interval")
	if err := parseFlagSet(set, args); err != nil {
		return commandResult{Command: "call.events"}, err
	}
	interval, err := time.ParseDuration(*intervalText)
	if err != nil || interval < 500*time.Millisecond || interval > 30*time.Second {
		return commandResult{Command: "call.events"}, usageError("--interval must be between 500ms and 30s")
	}
	client, clientErr := newAPIClient(options)
	if clientErr != nil {
		return commandResult{Command: "call.events"}, clientErr
	}
	if !*follow {
		response, requestErr := client.request(ctx, http.MethodGet, "/api/call/status", nil, "")
		if requestErr != nil {
			return commandResult{Command: "call.events"}, requestErr
		}
		return commandResult{Command: "call.events", Data: response.Body}, nil
	}

	last := ""
	sequence := 0
	warnings := options.Warnings
	for {
		response, requestErr := client.request(ctx, http.MethodGet, "/api/call/status", nil, "")
		if requestErr != nil {
			return commandResult{Command: "call.events", Streamed: true}, requestErr
		}
		current := string(response.Body)
		if current != last {
			sequence++
			if err := emitJSON(stdout, false, outputEnvelope{OK: true, Command: "call.events", Warnings: warnings, Data: map[string]any{
				"sequence": sequence, "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "state": response.Body,
			}}); err != nil {
				return commandResult{Command: "call.events", Streamed: true}, &commandError{Code: "output_failed", Message: err.Error(), ExitCode: 1}
			}
			warnings = nil
			last = current
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return commandResult{Command: "call.events", Streamed: true}, nil
		case <-timer.C:
		}
	}
}

func callDialCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	set := newFlagSet("call dial")
	number := set.String("number", "", "phone number")
	requestID := set.String("request-id", "", "idempotency key")
	dryRun := set.Bool("dry-run", false, "validate without dialing")
	yes := set.Bool("yes", false, "confirm dialing")
	if err := parseFlagSet(set, args); err != nil {
		return commandResult{Command: "call.dial"}, err
	}
	phone, validationErr := validatePhone(*number)
	if validationErr != nil {
		return commandResult{Command: "call.dial"}, validationErr
	}
	id, idErr := ensureRequestID("call", *requestID)
	if idErr != nil {
		return commandResult{Command: "call.dial"}, idErr
	}
	payload := map[string]string{"number": phone}
	if *dryRun {
		return dryRunResult("call.dial", "call.dial", payload, id), nil
	}
	if !*yes {
		return commandResult{Command: "call.dial", RequestID: id}, confirmationError("placing a call", id)
	}
	return mutateCommand(ctx, options, "call.dial", http.MethodPost, "/api/call/dial", payload, id)
}

func callAnswerCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	set := newFlagSet("call answer")
	requestID := set.String("request-id", "", "idempotency key")
	dryRun := set.Bool("dry-run", false, "validate without answering")
	yes := set.Bool("yes", false, "confirm answering")
	if err := parseFlagSet(set, args); err != nil {
		return commandResult{Command: "call.answer"}, err
	}
	id, idErr := ensureRequestID("answer", *requestID)
	if idErr != nil {
		return commandResult{Command: "call.answer"}, idErr
	}
	if *dryRun {
		return dryRunResult("call.answer", "call.answer", map[string]any{}, id), nil
	}
	if !*yes {
		return commandResult{Command: "call.answer", RequestID: id}, confirmationError("answering the current call", id)
	}
	return mutateCommand(ctx, options, "call.answer", http.MethodPost, "/api/call/answer", nil, id)
}

func callHangupCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	set := newFlagSet("call hangup")
	requestID := set.String("request-id", "", "idempotency key")
	dryRun := set.Bool("dry-run", false, "validate without hanging up")
	if err := parseFlagSet(set, args); err != nil {
		return commandResult{Command: "call.hangup"}, err
	}
	id, idErr := ensureRequestID("hangup", *requestID)
	if idErr != nil {
		return commandResult{Command: "call.hangup"}, idErr
	}
	if *dryRun {
		return dryRunResult("call.hangup", "call.hangup", map[string]any{}, id), nil
	}
	return mutateCommand(ctx, options, "call.hangup", http.MethodPost, "/api/call/hangup", nil, id)
}

func esimCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) == 0 {
		return commandResult{Command: "esim"}, usageError("usage: djonehub esim list|operation|switch")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return commandResult{Command: "esim.list"}, usageError("esim list does not accept arguments")
		}
		return getCommand(ctx, options, "esim.list", "/api/esim")
	case "operation":
		if len(args) != 1 {
			return commandResult{Command: "esim.operation"}, usageError("esim operation does not accept arguments")
		}
		return getCommand(ctx, options, "esim.operation", "/api/esim/operation")
	case "switch":
		return esimSwitchCommand(ctx, options, args[1:])
	default:
		return commandResult{Command: "esim"}, usageError("usage: djonehub esim list|operation|switch")
	}
}

func esimSwitchCommand(ctx context.Context, options globalOptions, args []string) (commandResult, *commandError) {
	set := newFlagSet("esim switch")
	iccid := set.String("iccid", "", "target ICCID")
	aid := set.String("aid", "", "optional profile AID")
	requestID := set.String("request-id", "", "idempotency key")
	dryRun := set.Bool("dry-run", false, "validate without switching")
	yes := set.Bool("yes", false, "confirm switching")
	if err := parseFlagSet(set, args); err != nil {
		return commandResult{Command: "esim.switch"}, err
	}
	target := strings.TrimSpace(*iccid)
	if len(target) < 10 || len(target) > 32 || strings.Trim(target, "0123456789") != "" {
		return commandResult{Command: "esim.switch"}, usageError("--iccid must contain 10 to 32 digits")
	}
	id, idErr := ensureRequestID("esim", *requestID)
	if idErr != nil {
		return commandResult{Command: "esim.switch"}, idErr
	}
	payload := map[string]string{"iccid": target, "aid": strings.TrimSpace(*aid)}
	if *dryRun {
		return dryRunResult("esim.switch", "esim.switch", payload, id), nil
	}
	if !*yes {
		return commandResult{Command: "esim.switch", RequestID: id}, confirmationError("switching the active eSIM profile", id)
	}
	client, clientErr := newAPIClient(options)
	if clientErr != nil {
		return commandResult{Command: "esim.switch", RequestID: id}, clientErr
	}
	overview, overviewErr := client.request(ctx, http.MethodGet, "/api/esim", nil, "")
	if overviewErr != nil {
		overviewErr.RequestID = id
		return commandResult{Command: "esim.switch", RequestID: id}, overviewErr
	}
	found, active := findESIMProfile(overlookJSON(overview.Body), target)
	if !found {
		return commandResult{Command: "esim.switch", RequestID: id}, &commandError{
			Code: "esim_profile_not_found", Message: "target ICCID is not present in the current eSIM overview", Recoverable: true, ExitCode: 5, RequestID: id,
		}
	}
	if active {
		return commandResult{Command: "esim.switch", RequestID: id, Data: map[string]any{
			"already_active": true, "target_iccid": target, "idempotency_replayed": false,
		}}, nil
	}
	return mutateWithClient(ctx, client, "esim.switch", http.MethodPost, "/api/esim/switch", payload, id)
}

func overlookJSON(raw json.RawMessage) any {
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

func findESIMProfile(value any, target string) (bool, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if iccid, _ := typed["iccid"].(string); iccid == target {
			state := typed["state"]
			active := state == float64(1) || state == "enabled" || state == "active"
			return true, active
		}
		for _, child := range typed {
			if found, active := findESIMProfile(child, target); found {
				return true, active
			}
		}
	case []any:
		for _, child := range typed {
			if found, active := findESIMProfile(child, target); found {
				return true, active
			}
		}
	}
	return false, false
}

func agentCommand(options globalOptions, args []string) (commandResult, *commandError) {
	if len(args) == 0 {
		return commandResult{Command: "agent"}, usageError("usage: djonehub agent guide|skill")
	}
	if args[0] == "guide" {
		if len(args) != 1 {
			return commandResult{Command: "agent.guide"}, usageError("agent guide does not accept arguments")
		}
		return commandResult{Command: "agent.guide", Data: map[string]string{
			"export_command": "djonehub agent skill export --format json",
			"prompt":         "请运行 djonehub agent skill export --format json，读取返回的 entrypoint_content、source_directory 和 files，并按照当前 Agent 支持的方式安装 Skill。如果当前 Agent 不支持安装 Skill，请直接读取并遵循 entrypoint_content。完成后，先运行 djonehub capabilities 确认可用能力。",
		}}, nil
	}
	if args[0] != "skill" || len(args) < 2 {
		return commandResult{Command: "agent"}, usageError("usage: djonehub agent guide|skill export|install|status|uninstall")
	}
	if args[1] == "export" {
		if len(args) != 2 {
			return commandResult{Command: "agent.skill.export"}, usageError("agent skill export does not accept arguments")
		}
		data, err := exportBundledSkill()
		return commandResult{Command: "agent.skill.export", Data: data}, err
	}
	set := newFlagSet("agent skill " + args[1])
	target := set.String("target", "auto", "auto, codex, claude, or agents")
	if err := parseFlagSet(set, args[2:]); err != nil {
		return commandResult{Command: "agent.skill." + args[1]}, err
	}
	switch args[1] {
	case "install":
		data, err := installBundledSkill(*target)
		return commandResult{Command: "agent.skill.install", Data: data}, err
	case "status":
		data, err := bundledSkillStatus(*target)
		return commandResult{Command: "agent.skill.status", Data: data}, err
	case "uninstall":
		data, err := uninstallBundledSkill(*target)
		return commandResult{Command: "agent.skill.uninstall", Data: data}, err
	default:
		return commandResult{Command: "agent.skill"}, usageError("usage: djonehub agent skill export|install|status|uninstall")
	}
}

func mutateCommand(ctx context.Context, options globalOptions, name, method, path string, body any, requestID string) (commandResult, *commandError) {
	client, err := newAPIClient(options)
	if err != nil {
		err.RequestID = requestID
		return commandResult{Command: name, RequestID: requestID}, err
	}
	return mutateWithClient(ctx, client, name, method, path, body, requestID)
}

func mutateWithClient(ctx context.Context, client *apiClient, name, method, path string, body any, requestID string) (commandResult, *commandError) {
	response, err := client.request(ctx, method, path, body, requestID)
	if err != nil {
		return commandResult{Command: name, RequestID: requestID}, err
	}
	return commandResult{Command: name, RequestID: requestID, Data: map[string]any{
		"result": response.Body, "idempotency_replayed": response.Replayed,
	}}, nil
}

func dryRunResult(command, scope string, payload any, requestID string) commandResult {
	return commandResult{Command: command, RequestID: requestID, Data: map[string]any{
		"dry_run": true, "required_scope": scope, "request": payload,
	}}
}

func confirmationError(action, requestID string) *commandError {
	return &commandError{
		Code:      "confirmation_required",
		Message:   "refusing " + action + " without --yes; use --dry-run first and pass --yes only after user authorization",
		RequestID: requestID, ExitCode: 2,
	}
}

func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

func parseFlagSet(set *flag.FlagSet, args []string) *commandError {
	if err := set.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if set.NArg() != 0 {
		return usageError("unexpected positional arguments: " + strings.Join(set.Args(), " "))
	}
	return nil
}

func readMessage(inline, path string) (string, *commandError) {
	if inline != "" && path != "" {
		return "", usageError("use exactly one of --message or --message-file")
	}
	message := inline
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", &commandError{Code: "message_file_unreadable", Message: err.Error(), ExitCode: 2}
		}
		if len(data) > 64<<10 {
			return "", usageError("--message-file must not exceed 64 KiB")
		}
		message = string(data)
	}
	if strings.TrimSpace(message) == "" {
		return "", usageError("one of --message or --message-file is required")
	}
	if len([]rune(message)) > 4096 {
		return "", usageError("message must not exceed 4096 characters")
	}
	return message, nil
}

func validatePhone(value string) (string, *commandError) {
	number := strings.TrimSpace(value)
	if number == "" {
		return "", usageError("phone number is required")
	}
	digits := number
	if strings.HasPrefix(digits, "+") {
		digits = digits[1:]
	}
	if len(digits) < 3 || len(digits) > 20 || strings.Trim(digits, "0123456789") != "" {
		return "", usageError("phone number must contain 3 to 20 digits with an optional leading +")
	}
	blocked := map[string]bool{"110": true, "112": true, "119": true, "120": true, "911": true, "999": true}
	if blocked[digits] {
		return "", &commandError{Code: "emergency_number_blocked", Message: "the AI CLI cannot dial or message emergency numbers", ExitCode: 2}
	}
	return number, nil
}

func ensureRequestID(prefix, value string) (string, *commandError) {
	if value != "" {
		if !requestIDPattern.MatchString(value) {
			return "", usageError("--request-id must be 8 to 128 characters using letters, digits, dot, underscore, colon, or hyphen")
		}
		return value, nil
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", &commandError{Code: "request_id_failed", Message: err.Error(), ExitCode: 1}
	}
	return prefix + "-" + hex.EncodeToString(buffer), nil
}
