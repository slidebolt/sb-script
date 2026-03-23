Feature: Advanced automation references

  Scenario: Occupancy timeout cancels pending shutoff when motion resumes
    Given the scripting engine is running
    And a binary sensor entity "test.motion.hall001" named "Hall Motion" class "motion" with state off
    And a light entity "test.hall.light001" named "Hall Light" with power off
    And a script definition "occupancy_timeout" is saved from file "occupancy_timeout.lua"
    And I start script "occupancy_timeout" with query ""
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.motion.hall001" changes to on
    Then within 500 milliseconds command "light_turn_on" reaches "test.hall.light001"
    When I clear observed commands
    And the binary sensor entity "test.motion.hall001" changes to off
    Then no command reaches "test.hall.light001" within 100 milliseconds
    When the binary sensor entity "test.motion.hall001" changes to on
    Then within 500 milliseconds command "light_turn_on" reaches "test.hall.light001"
    When I clear observed commands
    Then no command reaches "test.hall.light001" within 250 milliseconds

  Scenario: Manual override blocks motion automation until auto mode returns
    Given the scripting engine is running
    And a binary sensor entity "test.motion.manual001" named "Manual Motion" class "motion" with state off
    And a text entity "test.mode.override" named "Override Mode" with value "manual"
    And a light entity "test.override.light001" named "Override Light" with power off
    And a script definition "manual_override_lighting" is saved from file "manual_override_lighting.lua"
    And I start script "manual_override_lighting" with query ""
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.motion.manual001" changes to on
    Then no command reaches "test.override.light001" within 250 milliseconds
    When I clear observed commands
    And the text entity "test.mode.override" changes to "auto"
    And the binary sensor entity "test.motion.manual001" changes to off
    And the binary sensor entity "test.motion.manual001" changes to on
    Then within 500 milliseconds command "light_turn_on" reaches "test.override.light001"
    And within 500 milliseconds command "light_set_brightness" reaches "test.override.light001" with payload:
      """
      {"brightness":96}
      """

  Scenario: Scene cycler advances through multiple scenes on repeated button presses
    Given the scripting engine is running
    And a button entity "test.scene.button001" named "Scene Button"
    And a light entity "test.scene.light001" named "Scene Light" with color mode "rgb"
    And a script definition "scene_cycler_button" is saved from file "scene_cycler_button.lua"
    And I start script "scene_cycler_button" with query ""
    When I subscribe to commands on "test.>"
    And the button entity "test.scene.button001" is pressed
    Then within 500 milliseconds command "light_set_rgb" reaches "test.scene.light001" with payload:
      """
      {"r":255,"g":0,"b":0}
      """
    When I clear observed commands
    And the button entity "test.scene.button001" is pressed
    Then within 500 milliseconds command "light_set_rgb" reaches "test.scene.light001" with payload:
      """
      {"r":0,"g":255,"b":0}
      """
    When I clear observed commands
    And the button entity "test.scene.button001" is pressed
    Then within 500 milliseconds command "light_set_rgb" reaches "test.scene.light001" with payload:
      """
      {"r":0,"g":0,"b":255}
      """

  Scenario: Quiet-hours doorbell changes its behavior based on house mode
    Given the scripting engine is running
    And a button entity "test.entry.quietdoorbell" named "Quiet Doorbell"
    And a text entity "test.house.mode" named "House Mode" with value "quiet"
    And a light entity "test.house.porch001" named "Porch" with power off
    And a light entity "test.house.hall001" named "Hall" with power off
    And a lock entity "test.entry.lock001" named "Front Door" that is unlocked
    And a script definition "quiet_hours_doorbell" is saved from file "quiet_hours_doorbell.lua"
    And I start script "quiet_hours_doorbell" with query ""
    When I subscribe to commands on "test.>"
    And the button entity "test.entry.quietdoorbell" is pressed
    Then within 500 milliseconds command "light_set_brightness" reaches "test.house.porch001" with payload:
      """
      {"brightness":32}
      """
    And no command reaches "test.entry.lock001" within 250 milliseconds
    When I clear observed commands
    And the text entity "test.house.mode" changes to "normal"
    And the button entity "test.entry.quietdoorbell" is pressed
    Then within 500 milliseconds command "light_turn_on" reaches "test.house.hall001"
    And within 500 milliseconds command "lock_lock" reaches "test.entry.lock001"

  Scenario: Garage safety close waits for a second clear check before closing
    Given the scripting engine is running
    And a button entity "test.garage.requestclose" named "Close Garage"
    And a text entity "test.garage.safety" named "Garage Safety" with value "blocked"
    And a cover entity "test.garage.cover001" named "Garage Door" at position 100
    And a script definition "garage_safety_close" is saved from file "garage_safety_close.lua"
    And I start script "garage_safety_close" with query ""
    When I subscribe to commands on "test.>"
    And the button entity "test.garage.requestclose" is pressed
    Then no command reaches "test.garage.cover001" within 250 milliseconds
    When I clear observed commands
    And the text entity "test.garage.safety" changes to "clear"
    And the button entity "test.garage.requestclose" is pressed
    Then within 500 milliseconds command "cover_close" reaches "test.garage.cover001"

  Scenario: Presence arrival turns on only lights that are currently off
    Given the scripting engine is running
    And a text entity "test.presence.home" named "Presence" with value "away"
    And a light entity "test.arrival.light001" named "Entry Light" with power off
    And a light entity "test.arrival.light002" named "Lamp" with power on
    And a script definition "presence_arrival_lights" is saved from file "presence_arrival_lights.lua"
    And I start script "presence_arrival_lights" with query ""
    When I subscribe to commands on "test.>"
    And the text entity "test.presence.home" changes to "arrived"
    Then within 500 milliseconds command "light_turn_on" reaches "test.arrival.light001"
    And no command reaches "test.arrival.light002" within 250 milliseconds

  Scenario: Adaptive evening lighting chooses different scenes from the same sunset trigger
    Given the scripting engine is running
    And a text entity "solar.event.sun" named "Sun Event" with value "day"
    And a text entity "test.mode.scene" named "Scene Mode" with value "relax"
    And a light entity "test.evening.light001" named "Evening Light" with color mode "rgb"
    And a script definition "adaptive_evening_lighting" is saved from file "adaptive_evening_lighting.lua"
    And I start script "adaptive_evening_lighting" with query ""
    When I subscribe to commands on "test.>"
    And the text entity "solar.event.sun" changes to "sunset"
    Then within 500 milliseconds command "light_set_brightness" reaches "test.evening.light001" with payload:
      """
      {"brightness":80}
      """
    And within 500 milliseconds command "light_set_color_temp" reaches "test.evening.light001" with payload:
      """
      {"mireds":400}
      """
    When I clear observed commands
    And the text entity "test.mode.scene" changes to "night"
    And the text entity "solar.event.sun" changes to "day"
    And the text entity "solar.event.sun" changes to "sunset"
    Then within 500 milliseconds command "light_set_brightness" reaches "test.evening.light001" with payload:
      """
      {"brightness":20}
      """
    And within 500 milliseconds command "light_set_rgb" reaches "test.evening.light001" with payload:
      """
      {"r":255,"g":0,"b":0}
      """
