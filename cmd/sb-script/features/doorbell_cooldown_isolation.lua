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
