Automation("doorbell", {
  trigger = Entity("test.entry.doorbell001"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.presses > 0 then
    ctx.query("test.house.>"):each(function(e)
      if e.type == "light" then
        ctx.send(e, "light_turn_on", {})
      end
    end)
    ctx.send(ctx.queryOne("test.entry.lock001"), "lock_lock", {})
    ctx.send(ctx.queryOne("test.garage.cover001"), "cover_close", {})
  end
end)
