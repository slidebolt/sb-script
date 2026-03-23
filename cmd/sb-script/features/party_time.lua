Automation("party_time", {
  trigger = Interval({min=0.05, max=0.1}),
  targets = None(),
}, function(ctx)
  ctx.targets:each(function(e)
    if e.state.colorMode == "rgb" then
      ctx.send(e, "light_set_rgb", {r=255, g=0, b=255})
    end
  end)
end)
