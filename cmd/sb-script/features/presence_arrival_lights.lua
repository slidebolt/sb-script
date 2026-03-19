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
