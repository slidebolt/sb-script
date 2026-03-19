Automation("garage_safety_close", {
  trigger = Entity("test.garage.requestclose"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end

  if ctx.queryOne("test.garage.safety").state.value ~= "clear" then
    return
  end

  ctx.after(0.05, function(step)
    if step.queryOne("test.garage.safety").state.value == "clear" then
      step.send(step.queryOne("test.garage.cover001"), "cover_close", {})
    end
  end)
end)
