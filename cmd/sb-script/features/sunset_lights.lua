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
