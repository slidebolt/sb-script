local offset = 0
local palette = {
  {255, 128, 0},
  {255, 255, 0},
  {0, 255, 0},
  {0, 0, 255},
  {128, 0, 255},
  {255, 0, 0},
}

local function rainbow_segments(count, drift)
  local segments = {}
  for i = 1, count do
    local idx = ((i - 1 + drift) % #palette) + 1
    local color = palette[idx]
    segments[i] = {
      id = i,
      rgb = {color[1], color[2], color[3]},
      brightness = 180,
    }
  end
  return segments
end

Automation("lightstrip_rainbow", {
  trigger = Interval(0.05),
  targets = Query("?type=lightstrip"),
}, function(ctx)
  offset = (offset + 1) % #palette
  ctx.targets:each(function(strip)
    ctx.send(strip, "lightstrip_set_segments", {
      power = true,
      colorMode = "rgb",
      effect = "rainbow_drift",
      effectSpeed = 10,
      segments = rainbow_segments(4, offset),
    })
  end)
end)
