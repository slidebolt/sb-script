Automation("quiet_hours_doorbell", {
  trigger = Entity("test.entry.quietdoorbell"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses <= 0 then
    return
  end

  local quiet = ctx.queryOne("test.house.mode")
  if quiet ~= nil and quiet.state.value == "quiet" then
    ctx.send(ctx.queryOne("test.house.porch001"), "light_turn_on", {})
    ctx.send(ctx.queryOne("test.house.porch001"), "light_set_brightness", {brightness=32})
    return
  end

  ctx.query("test.house.>"):each(function(e)
    if e.type == "light" then
      ctx.send(e, "light_turn_on", {})
    end
  end)
  ctx.send(ctx.queryOne("test.entry.lock001"), "lock_lock", {})
end)
