# Scripting API

Language: `sb-script-lua`

Version: `1`

## Globals

### `Automation(name, spec, fn)`

Defines the entrypoint for one automation activation. The name must match the saved definition name when the automation is started.

**Parameters**

- `name` `string`: Definition name for this automation.
- `spec` `AutomationSpec`: Trigger and target configuration.
  - `trigger` `TriggerSpec`: Required trigger for the automation.
  - `targets` `TargetSpec`: Default target set. Omit or use None() when targets are resolved inside the callback.
- `fn` `function(ctx)`: Callback executed when the activation fires.

**Examples**

- `cmd/sb-script/features/party_time.lua`
- `cmd/sb-script/features/motion_lights.lua`

### `Script(name, [options], fn)`

Defines a script: fn runs once when an instance starts and may keep working with ctx.after. An instance is the script plus a scope (a saved query, its ctx.targets). Each declared scope gets a switch entity sb-script.script-<name>.<scope> (no scopes: sb-script.script-<name>.run) that is on while that instance runs; switch_turn_on starts it, switch_turn_off stops it gracefully. Automations get sb-script.automation-<name>.enabled; turning it off keeps the automation stopped across restarts.

**Parameters**

- `name` `string`: Definition name for this script.
- `options` `table`: Optional.
  - `scopes` `string[]`: Saved query names offered as switches, one entity each.
  - `stop` `function(ctx)`: Runs when the switch is turned off, with the instance's targets; may use ctx.after (up to 60 s). Not run by ctx.scripts:stop, stopAll or the stop API, which stop at once.
- `fn` `function(ctx)`: Runs once when the instance starts.

### `Entity(key)`

Creates an entity trigger or target spec that matches one exact entity key.

**Parameters**

- `key` `string`: Full entity key such as plugin.device.entity.

**Returns**: `TriggerSpec|TargetSpec`

**Examples**

- `cmd/sb-script/features/doorbell.lua`

### `Query(query)`

Creates an inline query-backed trigger or target spec. Queries are re-resolved at fire time.

**Parameters**

- `query` `string|table`: Storage search key pattern or structured query object.

**Returns**: `TriggerSpec|TargetSpec`

**Examples**

- `cmd/sb-script/features/motion_lights.lua`
- `cmd/sb-script/features/party_time.lua`

### `QueryRef(name)`

Creates a query-backed trigger or target spec from a named storage query resource.

**Parameters**

- `name` `string`: Stored query name under sb-query.queries.{name}.

**Returns**: `TriggerSpec|TargetSpec`

**Examples**

- `targets = QueryRef("rgb_lights")`

### `None()`

Creates an empty target spec for automations that resolve targets inside the callback.

**Returns**: `TargetSpec`

**Examples**

- `cmd/sb-script/features/doorbell.lua`

### `Interval(seconds | {min=seconds, max=seconds})`

Creates an interval trigger. The runtime clamps intervals below 50ms to 50ms.

**Parameters**

- `seconds` `number`: Fixed interval in seconds.
- `min` `number`: Minimum interval in seconds when using a range.
- `max` `number`: Maximum interval in seconds when using a range.

**Returns**: `TriggerSpec`

**Examples**

- `cmd/sb-script/features/party_time.lua`

## Context Methods

### `ctx.targets:each(fn)`

Iterates the current target entities for the activation firing.

**Parameters**

- `fn` `function(entity)`: Called for each target entity.

### `ctx.decision(label, data)`

Appends a structured decision/branch event to the automation log stream for the current trace.

**Parameters**

- `label` `string`: Short branch/decision label such as any_on_true or ignored_falling_edge.
- `data` `table`: Optional JSON-serializable fields explaining the decision.

### `ctx.send(entity, action, payload)`

Publishes a command subject for the given entity.

**Parameters**

- `entity` `entity`: Entity table returned by Query/Entity/ctx.targets.
- `action` `string`: Command action name.
- `payload` `table`: JSON-serializable command body.

### `ctx.query(query)`

Resolves entities from storage inside the callback.

**Parameters**

- `query` `string|table`: Storage search key pattern or structured query object.

**Returns**: `entities`

### `ctx.queryOne(query)`

Returns the first entity matching the query or nil.

**Parameters**

- `query` `string|table`: Storage search key pattern or structured query object.

**Returns**: `entity|nil`

### `ctx.after(seconds, fn)`

Schedules a one-shot timer owned by the activation.

**Parameters**

- `seconds` `number`: Delay in seconds.
- `fn` `function(ctx)`: Callback invoked after the delay.

**Returns**: `timer_id`

**Examples**

- `cmd/sb-script/features/fade_up.lua`

### `ctx.every(seconds, fn)`

Schedules a repeating timer owned by the activation.

**Parameters**

- `seconds` `number`: Repeat interval in seconds.
- `fn` `function(ctx)`: Callback invoked for each tick.

**Returns**: `timer_id`

**Examples**

- `cmd/sb-script/features/fade_down.lua`

### `ctx.cancel(timer_id)`

Cancels a timer created by ctx.after or ctx.every.

**Parameters**

- `timer_id` `number`: Timer identifier previously returned by ctx.after or ctx.every.

### `ctx.scripts:start(name, queryRef | {queryRef=...})`

Starts another stored script definition through the normal runtime contract.

**Parameters**

- `name` `string`: Stored script definition name.
- `queryRef` `string|table`: Optional query ref string or table containing queryRef.

### `ctx.scripts:stop(name, queryRef | {queryRef=...})`

Stops a running script instance through the normal runtime contract.

**Parameters**

- `name` `string`: Stored script definition name.
- `queryRef` `string|table`: Optional query ref string or table containing queryRef.

### `ctx.scripts:stopAll(queryRef | {queryRef=...})`

Stops all running script instances whose targets overlap with the given query ref.

**Parameters**

- `queryRef` `string|table`: Query ref string or table containing queryRef.

## Example Scripts

### `adaptive_evening_lighting.lua`

```lua
Automation("adaptive_evening_lighting", {
  trigger = Entity("solar.event.sun"),
  targets = Query("test.evening.>"),
}, function(ctx)
  if ctx.trigger.entity.state.value ~= "sunset" then
    return
  end

  local mode = ctx.queryOne("test.mode.scene")
  local value = "default"
  if mode ~= nil then
    value = mode.state.value
  end

  ctx.targets:each(function(e)
    if e.type ~= "light" then
      return
    end

    ctx.send(e, "light_turn_on", {})
    if value == "relax" then
      ctx.send(e, "light_set_brightness", {brightness=80})
      ctx.send(e, "light_set_color_temp", {mireds=400})
    elseif value == "night" then
      ctx.send(e, "light_set_brightness", {brightness=20})
      ctx.send(e, "light_set_rgb", {r=255, g=0, b=0})
    else
      ctx.send(e, "light_set_brightness", {brightness=160})
    end
  end)
end)
```

### `alarm_snooze_isolation.lua`

```lua
local count = 0

Automation("alarm_snooze_isolation", {
  trigger = Entity("test.multi.alarm"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.value ~= "ringing" then
    return
  end
  count = count + 1
  ctx.targets:each(function(e)
    ctx.send(e, "text_set_value", {value=tostring(count)})
  end)
end)
```

### `automation_core_interval_v1.lua`

```lua
Automation("party_time", {
  trigger = Interval(0.05),
  targets = None(),
}, function(ctx)
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_on", {})
  end)
end)
```

### `automation_core_interval_v2.lua`

```lua
Automation("party_time", {
  trigger = Interval(0.05),
  targets = None(),
}, function(ctx)
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_off", {})
  end)
end)
```

### `automation_core_restart.lua`

```lua
Automation("blink", {
  trigger = Interval(0.05),
  targets = Query("test.dev1.light001"),
}, function(ctx)
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_on", {})
  end)
end)
```

### `doorbell.lua`

```lua
Automation("doorbell", {
  trigger = Entity("test.entry.doorbell001"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses > 0 then
    ctx.query("test.house.>"):each(function(e)
      if e.type == "light" then
        ctx.send(e, "light_turn_on", {})
      end
    end)
    ctx.send(ctx.queryOne("test.entry.lock001"), "lock_lock", {})
    ctx.send(ctx.queryOne("test.garage.cover001"), "cover_close", {})
  end
end)
```

### `doorbell_cooldown_isolation.lua`

```lua
local cooling = false

Automation("doorbell_cooldown_isolation", {
  trigger = Entity("test.multi.doorbell"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end
  if cooling then
    return
  end
  cooling = true
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_on", {})
  end)
  ctx.after(0.25, function()
    cooling = false
  end)
end)
```

### `fade_down.lua`

```lua
local timers = {}
local active = false
local generation = 0

Automation("fade_down", {
  trigger = Entity("test.remote.fadedown"),
  targets = Query("test.lr.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    generation = generation + 1
    active = true
    local token = generation
    timers[1] = ctx.after(0.05, function(step)
      timers[2] = step.every(0.05, function(tick)
        if active and token == generation then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=-1})
          end)
        end
      end)
    end)
    timers[3] = ctx.after(0.10, function(step)
      timers[4] = step.every(0.05, function(tick)
        if active and token == generation then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=-5})
          end)
        end
      end)
    end)
    timers[5] = ctx.after(0.50, function(step)
      timers[6] = step.every(0.05, function(tick)
        if active and token == generation then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=-1})
          end)
        end
      end)
    end)
  else
    generation = generation + 1
    active = false
    for i = 1, #timers do
      ctx.cancel(timers[i])
    end
    timers = {}
  end
end)
```

### `fade_up.lua`

```lua
local timers = {}
local active = false
local generation = 0

Automation("fade_up", {
  trigger = Entity("test.remote.fadeup"),
  targets = Query("test.lr.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    generation = generation + 1
    active = true
    local token = generation
    timers[1] = ctx.after(0.05, function(step)
      timers[2] = step.every(0.05, function(tick)
        if active and token == generation then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=1})
          end)
        end
      end)
    end)
    timers[3] = ctx.after(0.10, function(step)
      timers[4] = step.every(0.05, function(tick)
        if active and token == generation then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=5})
          end)
        end
      end)
    end)
    timers[5] = ctx.after(0.50, function(step)
      timers[6] = step.every(0.05, function(tick)
        if active and token == generation then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=1})
          end)
        end
      end)
    end)
  else
    generation = generation + 1
    active = false
    for i = 1, #timers do
      ctx.cancel(timers[i])
    end
    timers = {}
  end
end)
```

### `garage_safety_close.lua`

```lua
Automation("garage_safety_close", {
  trigger = Entity("test.garage.requestclose"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end

  if ctx.queryOne("test.garage.safety").state.value ~= "clear" then
    return
  end

  ctx.after(0.05, function(step)
    if step.queryOne("test.garage.safety").state.value == "clear" then
      step.send(step.queryOne("test.garage.cover001"), "cover_close", {})
    end
  end)
end)
```

### `lightstrip_rainbow.lua`

```lua
local offset = 0
local palette = {
  {255, 128, 0},
  {255, 255, 0},
  {0, 255, 0},
  {0, 0, 255},
  {128, 0, 255},
  {255, 0, 0},
}

local function rainbow_segments(count, drift)
  local segments = {}
  for i = 1, count do
    local idx = ((i - 1 + drift) % #palette) + 1
    local color = palette[idx]
    segments[i] = {
      id = i,
      rgb = {color[1], color[2], color[3]},
      brightness = 180,
    }
  end
  return segments
end

Automation("lightstrip_rainbow", {
  trigger = Interval(0.05),
  targets = Query({
    where = {
      { field = "type", op = "eq", value = "lightstrip" },
    },
  }),
}, function(ctx)
  offset = (offset + 1) % #palette
  ctx.targets:each(function(strip)
    ctx.send(strip, "lightstrip_set_segments", {
      power = true,
      colorMode = "rgb",
      effect = "rainbow_drift",
      effectSpeed = 10,
      segments = rainbow_segments(4, offset),
    })
  end)
end)
```

### `manual_dimmer_hold_isolation.lua`

```lua
local phase = 0
local timers = {}
local active = false

Automation("manual_dimmer_hold_isolation", {
  trigger = Entity("test.multi.dim_up"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    active = true
    phase = phase + 1
    local delta = 1
    if phase >= 2 then
      delta = 5
    end
    timers[1] = ctx.after(0.05, function(step)
      if active then
        step.targets:each(function(e)
          step.send(e, "light_step_brightness", {delta=delta})
        end)
      end
    end)
  else
    active = false
    for i = 1, #timers do
      ctx.cancel(timers[i])
    end
    timers = {}
  end
end)
```

### `manual_override_lighting.lua`

```lua
Automation("manual_override_lighting", {
  trigger = Entity("test.motion.manual001"),
  targets = Query("test.override.>"),
}, function(ctx)
  if not ctx.trigger.entity.state.on then
    return
  end

  local mode = ctx.queryOne("test.mode.override")
  if mode ~= nil and mode.state.value == "manual" then
    return
  end

  ctx.targets:each(function(e)
    if e.type == "light" then
      ctx.send(e, "light_turn_on", {})
      ctx.send(e, "light_set_brightness", {brightness=96})
    end
  end)
end)
```

### `motion_lights.lua`

```lua
Automation("motion_lights", {
  trigger = Entity("test.motion.sensor001"),
  targets = Query("test.hall.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    ctx.targets:each(function(e)
      ctx.send(e, "light_set_brightness", {brightness=127})
      ctx.send(e, "light_set_color_temp", {mireds=370})
    end)
  else
    ctx.targets:each(function(e)
      ctx.send(e, "light_set_brightness", {brightness=25})
      ctx.send(e, "light_set_rgb", {r=255, g=0, b=0})
    end)
  end
end)
```

### `occupancy_timeout.lua`

```lua
local off_timer = nil

Automation("occupancy_timeout", {
  trigger = Query({
    where = {
      { field = "type", op = "eq", value = "binary_sensor" },
      { field = "state.deviceClass", op = "eq", value = "motion" },
    },
  }),
  targets = Query("test.hall.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    if off_timer then
      ctx.cancel(off_timer)
      off_timer = nil
    end
    ctx.targets:each(function(e)
      if e.type == "light" then
        ctx.send(e, "light_turn_on", {})
      end
    end)
  else
    if off_timer then
      ctx.cancel(off_timer)
    end
    off_timer = ctx.after(0.15, function(step)
      step.targets:each(function(e)
        if e.type == "light" then
          step.send(e, "light_turn_off", {})
        end
      end)
      off_timer = nil
    end)
  end
end)
```

### `occupancy_timeout_isolation.lua`

```lua
local pending = false
local timer = nil

Automation("occupancy_timeout_isolation", {
  trigger = Entity("test.multi.motion_sensor"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    if pending and timer then
      ctx.cancel(timer)
      ctx.targets:each(function(e)
        ctx.send(e, "text_set_value", {value="cancelled"})
      end)
    else
      ctx.targets:each(function(e)
        ctx.send(e, "text_set_value", {value="idle_on"})
      end)
    end
    pending = false
    timer = nil
    return
  end

  pending = true
  ctx.targets:each(function(e)
    ctx.send(e, "text_set_value", {value="pending"})
  end)
  timer = ctx.after(0.15, function(step)
    if pending then
      step.targets:each(function(e)
        step.send(e, "text_set_value", {value="expired"})
      end)
    end
    pending = false
    timer = nil
  end)
end)
```

### `party_time.lua`

```lua
Automation("party_time", {
  trigger = Interval({min=0.05, max=0.1}),
  targets = None(),
}, function(ctx)
  ctx.targets:each(function(e)
    if e.state.colorMode == "rgb" then
      ctx.send(e, "light_set_rgb", {r=255, g=0, b=255})
    end
  end)
end)
```

### `presence_arrival_isolation.lua`

```lua
local greeted = false

Automation("presence_arrival_isolation", {
  trigger = Entity("test.multi.presence"),
  targets = None(),
}, function(ctx)
  local value = ctx.trigger.entity.state.value
  if value == "away" then
    greeted = false
    return
  end
  if value ~= "arrived" then
    return
  end
  if greeted then
    return
  end
  greeted = true
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_on", {})
  end)
end)
```

### `presence_arrival_lights.lua`

```lua
Automation("presence_arrival_lights", {
  trigger = Entity("test.presence.home"),
  targets = Query("test.arrival.>"),
}, function(ctx)
  if ctx.trigger.entity.state.value ~= "arrived" then
    return
  end

  ctx.targets:each(function(e)
    if e.type == "light" and not e.state.power then
      ctx.send(e, "light_turn_on", {})
    end
  end)
end)
```

### `quiet_hours_doorbell.lua`

```lua
Automation("quiet_hours_doorbell", {
  trigger = Entity("test.entry.quietdoorbell"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end

  local quiet = ctx.queryOne("test.house.mode")
  if quiet ~= nil and quiet.state.value == "quiet" then
    ctx.send(ctx.queryOne("test.house.porch001"), "light_turn_on", {})
    ctx.send(ctx.queryOne("test.house.porch001"), "light_set_brightness", {brightness=32})
    return
  end

  ctx.query("test.house.>"):each(function(e)
    if e.type == "light" then
      ctx.send(e, "light_turn_on", {})
    end
  end)
  ctx.send(ctx.queryOne("test.entry.lock001"), "lock_lock", {})
end)
```

### `scene_cycler_button.lua`

```lua
local index = 0
local scenes = {
  {r=255, g=0, b=0},
  {r=0, g=255, b=0},
  {r=0, g=0, b=255},
}

Automation("scene_cycler_button", {
  trigger = Entity("test.scene.button001"),
  targets = Query("test.scene.>"),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end

  index = (index % #scenes) + 1
  local scene = scenes[index]
  ctx.targets:each(function(e)
    if e.type == "light" then
      ctx.send(e, "light_set_rgb", scene)
    end
  end)
end)
```

### `scene_cycler_isolation.lua`

```lua
local index = 0
local scenes = {
  {r=255, g=0, b=0},
  {r=0, g=255, b=0},
  {r=0, g=0, b=255},
}

Automation("scene_cycler_isolation", {
  trigger = Entity("test.multi.scene_button"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end
  index = (index % #scenes) + 1
  local scene = scenes[index]
  ctx.targets:each(function(e)
    ctx.send(e, "light_set_rgb", scene)
  end)
end)
```

### `sunset_lights.lua`

```lua
Automation("sunset_lights", {
  trigger = Entity("solar.event.sun"),
  targets = Query("test.porch.>"),
}, function(ctx)
  if ctx.trigger.entity.state.value == "sunset" then
    ctx.targets:each(function(e)
      ctx.send(e, "light_turn_on", {})
    end)
  end
end)
```

### `wake_up.lua`

```lua
Automation("wake_up", {
  trigger = Entity("alarm.clock.main"),
  targets = Query("test.bedroom.>"),
}, function(ctx)
  if ctx.trigger.entity.state.value == "ringing" then
    ctx.targets:each(function(e)
      ctx.send(e, "light_turn_on", {})
      ctx.send(e, "light_set_brightness", {brightness=64})
    end)
  end
end)
```

