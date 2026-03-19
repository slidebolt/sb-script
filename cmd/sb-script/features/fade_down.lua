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
