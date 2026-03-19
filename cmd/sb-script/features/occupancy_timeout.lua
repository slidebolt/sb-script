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
