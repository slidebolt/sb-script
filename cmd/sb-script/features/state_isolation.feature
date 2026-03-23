Feature: Per-activation Lua state isolation

  Scenario: Scene cycler keeps separate scene index per started group
    Given the scripting engine is running
    And a button entity "test.multi.scene_button" named "Scene Button"
    And a light entity "test.isoa.light001" named "Group A" with color mode "rgb"
    And a light entity "test.isob.light001" named "Group B" with color mode "rgb"
    And a light entity "test.isoc.light001" named "Group C" with color mode "rgb"
    And a script definition "scene_cycler_isolation" is saved from file "scene_cycler_isolation.lua"
    And I start script "scene_cycler_isolation" with query "test.isoa.>"
    When I subscribe to commands on "test.>"
    And the button entity "test.multi.scene_button" is pressed
    Then within 500 milliseconds command "light_set_rgb" reaches "test.isoa.light001" with payload:
      """
      {"r":255,"g":0,"b":0}
      """
    When I clear observed commands
    And I start script "scene_cycler_isolation" with query "test.isob.>"
    And I start script "scene_cycler_isolation" with query "test.isoc.>"
    And the button entity "test.multi.scene_button" is pressed
    Then within 500 milliseconds command "light_set_rgb" reaches "test.isoa.light001" with payload:
      """
      {"r":0,"g":255,"b":0}
      """
    And within 500 milliseconds command "light_set_rgb" reaches "test.isob.light001" with payload:
      """
      {"r":255,"g":0,"b":0}
      """
    And within 500 milliseconds command "light_set_rgb" reaches "test.isoc.light001" with payload:
      """
      {"r":255,"g":0,"b":0}
      """

  Scenario: Occupancy timeout keeps pending timeout state per activation
    Given the scripting engine is running
    And a binary sensor entity "test.multi.motion_sensor" named "Motion" class "motion" with state on
    And a text entity "test.isoa.text001" named "Group A Status" with value "idle"
    And a text entity "test.isob.text001" named "Group B Status" with value "idle"
    And a script definition "occupancy_timeout_isolation" is saved from file "occupancy_timeout_isolation.lua"
    And I start script "occupancy_timeout_isolation" with query "test.isoa.>"
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.multi.motion_sensor" changes to off
    Then within 500 milliseconds command "text_set_value" reaches "test.isoa.text001" with payload:
      """
      {"value":"pending"}
      """
    When I clear observed commands
    And I start script "occupancy_timeout_isolation" with query "test.isob.>"
    And the binary sensor entity "test.multi.motion_sensor" changes to on
    Then within 500 milliseconds command "text_set_value" reaches "test.isoa.text001" with payload:
      """
      {"value":"cancelled"}
      """
    And within 500 milliseconds command "text_set_value" reaches "test.isob.text001" with payload:
      """
      {"value":"idle_on"}
      """

  Scenario: Manual dimmer hold phase stays separate per activation
    Given the scripting engine is running
    And a binary sensor entity "test.multi.dim_up" named "Dim Up" class "button" with state off
    And a light entity "test.isoa.light001" named "Group A" with power off
    And a light entity "test.isob.light001" named "Group B" with power off
    And a script definition "manual_dimmer_hold_isolation" is saved from file "manual_dimmer_hold_isolation.lua"
    And I start script "manual_dimmer_hold_isolation" with query "test.isoa.>"
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.multi.dim_up" changes to on
    Then within 500 milliseconds command "light_step_brightness" reaches "test.isoa.light001" with payload:
      """
      {"delta":1}
      """
    When I clear observed commands
    And the binary sensor entity "test.multi.dim_up" changes to off
    And I start script "manual_dimmer_hold_isolation" with query "test.isob.>"
    And the binary sensor entity "test.multi.dim_up" changes to on
    Then within 500 milliseconds command "light_step_brightness" reaches "test.isoa.light001" with payload:
      """
      {"delta":5}
      """
    And within 500 milliseconds command "light_step_brightness" reaches "test.isob.light001" with payload:
      """
      {"delta":1}
      """

  Scenario: Alarm snooze count is isolated per activation
    Given the scripting engine is running
    And a text entity "test.multi.alarm" named "Alarm" with value "idle"
    And a text entity "test.isoa.text001" named "Group A Alarm" with value "0"
    And a text entity "test.isob.text001" named "Group B Alarm" with value "0"
    And a text entity "test.isoc.text001" named "Group C Alarm" with value "0"
    And a script definition "alarm_snooze_isolation" is saved from file "alarm_snooze_isolation.lua"
    And I start script "alarm_snooze_isolation" with query "test.isoa.>"
    When I subscribe to commands on "test.>"
    And the text entity "test.multi.alarm" changes to "ringing"
    Then within 500 milliseconds command "text_set_value" reaches "test.isoa.text001" with payload:
      """
      {"value":"1"}
      """
    When I clear observed commands
    And I start script "alarm_snooze_isolation" with query "test.isob.>"
    And I start script "alarm_snooze_isolation" with query "test.isoc.>"
    And the text entity "test.multi.alarm" changes to "idle"
    And the text entity "test.multi.alarm" changes to "ringing"
    Then within 500 milliseconds command "text_set_value" reaches "test.isoa.text001" with payload:
      """
      {"value":"2"}
      """
    And within 500 milliseconds command "text_set_value" reaches "test.isob.text001" with payload:
      """
      {"value":"1"}
      """
    And within 500 milliseconds command "text_set_value" reaches "test.isoc.text001" with payload:
      """
      {"value":"1"}
      """

  Scenario: Doorbell cooldown state is isolated per activation
    Given the scripting engine is running
    And a button entity "test.multi.doorbell" named "Doorbell"
    And a light entity "test.isoa.light001" named "Group A" with power off
    And a light entity "test.isob.light001" named "Group B" with power off
    And a script definition "doorbell_cooldown_isolation" is saved from file "doorbell_cooldown_isolation.lua"
    And I start script "doorbell_cooldown_isolation" with query "test.isoa.>"
    When I subscribe to commands on "test.>"
    And the button entity "test.multi.doorbell" is pressed
    Then within 500 milliseconds command "light_turn_on" reaches "test.isoa.light001"
    When I clear observed commands
    And I start script "doorbell_cooldown_isolation" with query "test.isob.>"
    And the button entity "test.multi.doorbell" is pressed
    Then no command reaches "test.isoa.light001" within 250 milliseconds
    And within 500 milliseconds command "light_turn_on" reaches "test.isob.light001"

  Scenario: Presence arrival greeted state is isolated per activation
    Given the scripting engine is running
    And a text entity "test.multi.presence" named "Presence" with value "away"
    And a light entity "test.isoa.light001" named "Group A" with power off
    And a light entity "test.isob.light001" named "Group B" with power off
    And a script definition "presence_arrival_isolation" is saved from file "presence_arrival_isolation.lua"
    And I start script "presence_arrival_isolation" with query "test.isoa.>"
    When I subscribe to commands on "test.>"
    And the text entity "test.multi.presence" changes to "arrived"
    Then within 500 milliseconds command "light_turn_on" reaches "test.isoa.light001"
    When I clear observed commands
    And I start script "presence_arrival_isolation" with query "test.isob.>"
    And the text entity "test.multi.presence" changes to "away"
    And the text entity "test.multi.presence" changes to "arrived"
    Then within 500 milliseconds command "light_turn_on" reaches "test.isoa.light001"
    And within 500 milliseconds command "light_turn_on" reaches "test.isob.light001"
    When I clear observed commands
    And I wait 50 milliseconds
    And I clear observed commands
    And the text entity "test.multi.presence" changes to "arrived"
    Then no command reaches "test.isoa.light001" within 250 milliseconds
    And no command reaches "test.isob.light001" within 250 milliseconds
