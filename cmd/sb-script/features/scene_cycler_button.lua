local index = 0
local scenes = {
  {r=255, g=0, b=0},
  {r=0, g=255, b=0},
  {r=0, g=0, b=255},
}

Automation("scene_cycler_button", {
  trigger = Entity("test.scene.button001"),
  targets = Query("test.scene.>"),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end

  index = (index % #scenes) + 1
  local scene = scenes[index]
  ctx.targets:each(function(e)
    if e.type == "light" then
      ctx.send(e, "light_set_rgb", scene)
    end
  end)
end)
