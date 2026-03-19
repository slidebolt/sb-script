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
