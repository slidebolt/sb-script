local count = 0

Automation("alarm_snooze_isolation", {
  trigger = Entity("test.multi.alarm"),
  targets = None(),
}, function(ctx)
  if ctx.trigger.entity.state.value ~= "ringing" then
    return
  end
  count = count + 1
  ctx.targets:each(function(e)
    ctx.send(e, "text_set_value", {value=tostring(count)})
  end)
end)
