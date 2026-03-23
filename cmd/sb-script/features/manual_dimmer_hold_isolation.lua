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
