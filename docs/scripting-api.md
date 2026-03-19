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

### `Entity(key)`

Creates an entity trigger or target spec that matches one exact entity key.

**Parameters**

- `key` `string`: Full entity key such as plugin.device.entity.

**Returns**: `TriggerSpec|TargetSpec`

**Examples**

- `cmd/sb-script/features/doorbell.lua`

### `Query(query)`

Creates a query-backed trigger or target spec. Queries are re-resolved at fire time.

**Parameters**

- `query` `string`: Storage search key pattern or filter query.

**Returns**: `TriggerSpec|TargetSpec`

**Examples**

- `cmd/sb-script/features/motion_lights.lua`
- `cmd/sb-script/features/party_time.lua`

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

### `ctx.send(entity, action, payload)`

Publishes a command subject for the given entity.

**Parameters**

- `entity` `entity`: Entity table returned by Query/Entity/ctx.targets.
- `action` `string`: Command action name.
- `payload` `table`: JSON-serializable command body.

### `ctx.query(query)`

Resolves entities from storage inside the callback.

**Parameters**

- `query` `string`: Storage search key pattern or filter query.

**Returns**: `entities`

### `ctx.queryOne(query)`

Returns the first entity matching the query or nil.

**Parameters**

- `query` `string`: Storage search key pattern or filter query.

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

### `fade_down.lua`

```lua
local timers = {}
local active = false

Automation("fade_down", {
  trigger = Entity("test.remote.fadedown"),
  targets = Query("test.lr.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    active = true
    timers[1] = ctx.after(0.05, function(step)
      timers[2] = step.every(0.05, function(tick)
        if active then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=-1})
          end)
        end
      end)
    end)
    timers[3] = ctx.after(0.10, function(step)
      timers[4] = step.every(0.05, function(tick)
        if active then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=-5})
          end)
        end
      end)
    end)
    timers[5] = ctx.after(0.50, function(step)
      timers[6] = step.every(0.05, function(tick)
        if active then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=-1})
          end)
        end
      end)
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

### `fade_up.lua`

```lua
local timers = {}
local active = false

Automation("fade_up", {
  trigger = Entity("test.remote.fadeup"),
  targets = Query("test.lr.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    active = true
    timers[1] = ctx.after(0.05, function(step)
      timers[2] = step.every(0.05, function(tick)
        if active then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=1})
          end)
        end
      end)
    end)
    timers[3] = ctx.after(0.10, function(step)
      timers[4] = step.every(0.05, function(tick)
        if active then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=5})
          end)
        end
      end)
    end)
    timers[5] = ctx.after(0.50, function(step)
      timers[6] = step.every(0.05, function(tick)
        if active then
          tick.targets:each(function(e)
            tick.send(e, "light_step_brightness", {delta=1})
          end)
        end
      end)
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
  trigger = Query("?type=binary_sensor state.deviceClass=motion"),
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

