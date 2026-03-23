package engine

type ParamDoc struct {
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Required    bool       `json:"required,omitempty"`
	Fields      []ParamDoc `json:"fields,omitempty"`
}

type APIDoc struct {
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Signature   string     `json:"signature"`
	Description string     `json:"description"`
	Params      []ParamDoc `json:"params,omitempty"`
	Returns     string     `json:"returns,omitempty"`
	Examples    []string   `json:"examples,omitempty"`
}

type APIReference struct {
	Language       string   `json:"language"`
	Version        int      `json:"version"`
	Globals        []APIDoc `json:"globals"`
	ContextMethods []APIDoc `json:"context_methods"`
}

func APIReferenceDoc() APIReference {
	return APIReference{
		Language: "sb-script-lua",
		Version:  1,
		Globals: []APIDoc{
			{
				Name:        "Automation",
				Kind:        "global",
				Signature:   "Automation(name, spec, fn)",
				Description: "Defines the entrypoint for one automation activation. The name must match the saved definition name when the automation is started.",
				Params: []ParamDoc{
					{Name: "name", Type: "string", Description: "Definition name for this automation.", Required: true},
					{Name: "spec", Type: "AutomationSpec", Description: "Trigger and target configuration.", Required: true, Fields: []ParamDoc{
						{Name: "trigger", Type: "TriggerSpec", Description: "Required trigger for the automation.", Required: true},
						{Name: "targets", Type: "TargetSpec", Description: "Default target set. Omit or use None() when targets are resolved inside the callback."},
					}},
					{Name: "fn", Type: "function(ctx)", Description: "Callback executed when the activation fires.", Required: true},
				},
				Examples: []string{
					"cmd/sb-script/features/party_time.lua",
					"cmd/sb-script/features/motion_lights.lua",
				},
			},
			{
				Name:        "Entity",
				Kind:        "global",
				Signature:   "Entity(key)",
				Description: "Creates an entity trigger or target spec that matches one exact entity key.",
				Params: []ParamDoc{
					{Name: "key", Type: "string", Description: "Full entity key such as plugin.device.entity.", Required: true},
				},
				Returns:  "TriggerSpec|TargetSpec",
				Examples: []string{"cmd/sb-script/features/doorbell.lua"},
			},
			{
				Name:        "Query",
				Kind:        "global",
				Signature:   "Query(query)",
				Description: "Creates an inline query-backed trigger or target spec. Queries are re-resolved at fire time.",
				Params: []ParamDoc{
					{Name: "query", Type: "string|table", Description: "Storage search key pattern or structured query object.", Required: true},
				},
				Returns: "TriggerSpec|TargetSpec",
				Examples: []string{
					"cmd/sb-script/features/motion_lights.lua",
					"cmd/sb-script/features/party_time.lua",
				},
			},
			{
				Name:        "QueryRef",
				Kind:        "global",
				Signature:   "QueryRef(name)",
				Description: "Creates a query-backed trigger or target spec from a named storage query resource.",
				Params: []ParamDoc{
					{Name: "name", Type: "string", Description: "Stored query name under sb-query.queries.{name}.", Required: true},
				},
				Returns:  "TriggerSpec|TargetSpec",
				Examples: []string{"targets = QueryRef(\"rgb_lights\")"},
			},
			{
				Name:        "None",
				Kind:        "global",
				Signature:   "None()",
				Description: "Creates an empty target spec for automations that resolve targets inside the callback.",
				Returns:     "TargetSpec",
				Examples:    []string{"cmd/sb-script/features/doorbell.lua"},
			},
			{
				Name:        "Interval",
				Kind:        "global",
				Signature:   "Interval(seconds | {min=seconds, max=seconds})",
				Description: "Creates an interval trigger. The runtime clamps intervals below 50ms to 50ms.",
				Params: []ParamDoc{
					{Name: "seconds", Type: "number", Description: "Fixed interval in seconds."},
					{Name: "min", Type: "number", Description: "Minimum interval in seconds when using a range."},
					{Name: "max", Type: "number", Description: "Maximum interval in seconds when using a range."},
				},
				Returns:  "TriggerSpec",
				Examples: []string{"cmd/sb-script/features/party_time.lua"},
			},
		},
		ContextMethods: []APIDoc{
			{
				Name:        "targets:each",
				Kind:        "context_method",
				Signature:   "ctx.targets:each(fn)",
				Description: "Iterates the current target entities for the activation firing.",
				Params: []ParamDoc{
					{Name: "fn", Type: "function(entity)", Description: "Called for each target entity.", Required: true},
				},
			},
			{
				Name:        "ctx.send",
				Kind:        "context_method",
				Signature:   "ctx.send(entity, action, payload)",
				Description: "Publishes a command subject for the given entity.",
				Params: []ParamDoc{
					{Name: "entity", Type: "entity", Description: "Entity table returned by Query/Entity/ctx.targets.", Required: true},
					{Name: "action", Type: "string", Description: "Command action name.", Required: true},
					{Name: "payload", Type: "table", Description: "JSON-serializable command body."},
				},
			},
			{
				Name:        "ctx.query",
				Kind:        "context_method",
				Signature:   "ctx.query(query)",
				Description: "Resolves entities from storage inside the callback.",
				Params: []ParamDoc{
					{Name: "query", Type: "string|table", Description: "Storage search key pattern or structured query object.", Required: true},
				},
				Returns: "entities",
			},
			{
				Name:        "ctx.queryOne",
				Kind:        "context_method",
				Signature:   "ctx.queryOne(query)",
				Description: "Returns the first entity matching the query or nil.",
				Params: []ParamDoc{
					{Name: "query", Type: "string|table", Description: "Storage search key pattern or structured query object.", Required: true},
				},
				Returns: "entity|nil",
			},
			{
				Name:        "ctx.after",
				Kind:        "context_method",
				Signature:   "ctx.after(seconds, fn)",
				Description: "Schedules a one-shot timer owned by the activation.",
				Params: []ParamDoc{
					{Name: "seconds", Type: "number", Description: "Delay in seconds.", Required: true},
					{Name: "fn", Type: "function(ctx)", Description: "Callback invoked after the delay.", Required: true},
				},
				Returns:  "timer_id",
				Examples: []string{"cmd/sb-script/features/fade_up.lua"},
			},
			{
				Name:        "ctx.every",
				Kind:        "context_method",
				Signature:   "ctx.every(seconds, fn)",
				Description: "Schedules a repeating timer owned by the activation.",
				Params: []ParamDoc{
					{Name: "seconds", Type: "number", Description: "Repeat interval in seconds.", Required: true},
					{Name: "fn", Type: "function(ctx)", Description: "Callback invoked for each tick.", Required: true},
				},
				Returns:  "timer_id",
				Examples: []string{"cmd/sb-script/features/fade_down.lua"},
			},
			{
				Name:        "ctx.cancel",
				Kind:        "context_method",
				Signature:   "ctx.cancel(timer_id)",
				Description: "Cancels a timer created by ctx.after or ctx.every.",
				Params: []ParamDoc{
					{Name: "timer_id", Type: "number", Description: "Timer identifier previously returned by ctx.after or ctx.every.", Required: true},
				},
			},
			{
				Name:        "ctx.scripts:start",
				Kind:        "context_method",
				Signature:   "ctx.scripts:start(name, queryRef | {queryRef=...})",
				Description: "Starts another stored script definition through the normal runtime contract.",
				Params: []ParamDoc{
					{Name: "name", Type: "string", Description: "Stored script definition name.", Required: true},
					{Name: "queryRef", Type: "string|table", Description: "Optional query ref string or table containing queryRef."},
				},
			},
			{
				Name:        "ctx.scripts:stop",
				Kind:        "context_method",
				Signature:   "ctx.scripts:stop(name, queryRef | {queryRef=...})",
				Description: "Stops a running script instance through the normal runtime contract.",
				Params: []ParamDoc{
					{Name: "name", Type: "string", Description: "Stored script definition name.", Required: true},
					{Name: "queryRef", Type: "string|table", Description: "Optional query ref string or table containing queryRef."},
				},
			},
		},
	}
}
