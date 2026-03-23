Automation("blink", {
  trigger = Interval(0.05),
  targets = Query("test.dev1.light001"),
}, function(ctx)
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_on", {})
  end)
end)
