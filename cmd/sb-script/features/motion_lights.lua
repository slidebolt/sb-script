Automation("motion_lights", {
  trigger = Entity("test.motion.sensor001"),
  targets = Query("test.hall.>"),
}, function(ctx)
  if ctx.trigger.entity.state.on then
    ctx.targets:each(function(e)
      ctx.send(e, "light_set_brightness", {brightness=127})
      ctx.send(e, "light_set_color_temp", {mireds=370})
    end)
  else
    ctx.targets:each(function(e)
      ctx.send(e, "light_set_brightness", {brightness=25})
      ctx.send(e, "light_set_rgb", {r=255, g=0, b=0})
    end)
  end
end)
