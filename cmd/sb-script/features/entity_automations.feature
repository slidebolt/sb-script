Feature: Entity-driven automations

  Scenario: Motion detection automation changes lights for motion and clear
    Given the scripting engine is running
    And a binary sensor entity "test.motion.sensor001" named "Hall Motion" class "motion" with state off
    And a light entity "test.hall.light001" named "Hall Light A" with color mode "rgb"
    And a light entity "test.hall.light002" named "Hall Light B" with color mode "rgb"
    And a script definition "motion_lights" is saved from file "motion_lights.lua"
    And I start script "motion_lights" with query ""
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.motion.sensor001" changes to on
    Then within 500 milliseconds command "light_set_brightness" reaches "test.hall.light001" with payload:
      """
      {"brightness":127}
      """
    And within 500 milliseconds command "light_set_color_temp" reaches "test.hall.light002" with payload:
      """
      {"mireds":370}
      """
    When I clear observed commands
    And the binary sensor entity "test.motion.sensor001" changes to off
    Then within 500 milliseconds command "light_set_brightness" reaches "test.hall.light001" with payload:
      """
      {"brightness":25}
      """
    And within 500 milliseconds command "light_set_rgb" reaches "test.hall.light002" with payload:
      """
      {"r":255,"g":0,"b":0}
      """

  Scenario: Doorbell automation turns on lights, locks the door, and closes the garage
    Given the scripting engine is running
    And a button entity "test.entry.doorbell001" named "Doorbell"
    And a light entity "test.house.light001" named "Porch" with power off
    And a light entity "test.house.light002" named "Hall" with power off
    And a lock entity "test.entry.lock001" named "Front Door" that is unlocked
    And a cover entity "test.garage.cover001" named "Garage Door" at position 100
    And a script definition "doorbell" is saved from file "doorbell.lua"
    And I start script "doorbell" with query ""
    When I subscribe to commands on "test.>"
    And the button entity "test.entry.doorbell001" is pressed
    Then within 500 milliseconds command "light_turn_on" reaches "test.house.light001"
    And within 500 milliseconds command "light_turn_on" reaches "test.house.light002"
    And within 500 milliseconds command "lock_lock" reaches "test.entry.lock001"
    And within 500 milliseconds command "cover_close" reaches "test.garage.cover001"

  Scenario: Solar event entity can trigger a sunset automation
    Given the scripting engine is running
    And a text entity "solar.event.sun" named "Sun Event" with value "day"
    And a light entity "test.porch.light001" named "Porch" with power off
    And a script definition "sunset_lights" is saved from file "sunset_lights.lua"
    And I start script "sunset_lights" with query ""
    When I subscribe to commands on "test.>"
    And the text entity "solar.event.sun" changes to "sunset"
    Then within 500 milliseconds command "light_turn_on" reaches "test.porch.light001"

  Scenario: Wakeup alarm entity can trigger a wakeup automation
    Given the scripting engine is running
    And a text entity "alarm.clock.main" named "Alarm" with value "idle"
    And a light entity "test.bedroom.light001" named "Bedroom" with power off
    And a script definition "wake_up" is saved from file "wake_up.lua"
    And I start script "wake_up" with query ""
    When I subscribe to commands on "test.>"
    And the text entity "alarm.clock.main" changes to "ringing"
    Then within 500 milliseconds command "light_turn_on" reaches "test.bedroom.light001"
    And within 500 milliseconds command "light_set_brightness" reaches "test.bedroom.light001" with payload:
      """
      {"brightness":64}
      """
