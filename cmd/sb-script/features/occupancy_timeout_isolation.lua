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
