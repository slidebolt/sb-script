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
