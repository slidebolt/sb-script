Automation("adaptive_evening_lighting", {
  trigger = Entity("solar.event.sun"),
  targets = Query("test.evening.>"),
}, function(ctx)
  if ctx.trigger.entity.state.value ~= "sunset" then
    return
  end

  local mode = ctx.queryOne("test.mode.scene")
  local value = "default"
  if mode ~= nil then
    value = mode.state.value
  end

  ctx.targets:each(function(e)
    if e.type ~= "light" then
      return
    end

    ctx.send(e, "light_turn_on", {})
    if value == "relax" then
      ctx.send(e, "light_set_brightness", {brightness=80})
      ctx.send(e, "light_set_color_temp", {mireds=400})
    elseif value == "night" then
      ctx.send(e, "light_set_brightness", {brightness=20})
      ctx.send(e, "light_set_rgb", {r=255, g=0, b=0})
    else
      ctx.send(e, "light_set_brightness", {brightness=160})
    end
  end)
end)
