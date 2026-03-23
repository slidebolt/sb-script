Automation("party_time", {
  trigger = Interval(0.05),
  targets = None(),
}, function(ctx)
  ctx.targets:each(function(e)
    ctx.send(e, "light_turn_off", {})
  end)
end)
