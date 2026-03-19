Automation("manual_override_lighting", {
  trigger = Entity("test.motion.manual001"),
  targets = Query("test.override.>"),
}, function(ctx)
  if not ctx.trigger.entity.state.on then
    return
  end

  local mode = ctx.queryOne("test.mode.override")
  if mode ~= nil and mode.state.value == "manual" then
    return
  end

  ctx.targets:each(function(e)
    if e.type == "light" then
      ctx.send(e, "light_turn_on", {})
      ctx.send(e, "light_set_brightness", {brightness=96})
    end
  end)
end)
