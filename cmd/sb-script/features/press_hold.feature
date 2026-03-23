Feature: Press and hold automations

  Scenario: Fade up while held and stop on release
    Given the scripting engine is running
    And a binary sensor entity "test.remote.fadeup" named "Fade Up" class "button" with state off
    And a light entity "test.lr.light001" named "Living Room" with power off
    And a script definition "fade_up" is saved from file "fade_up.lua"
    And I start script "fade_up" with query ""
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.remote.fadeup" changes to on
    Then within 400 milliseconds command "light_step_brightness" reaches "test.lr.light001" with payload:
      """
      {"delta":1}
      """
    And within 400 milliseconds command "light_step_brightness" reaches "test.lr.light001" with payload:
      """
      {"delta":5}
      """
    When I clear observed commands
    And the binary sensor entity "test.remote.fadeup" changes to off
    And I wait 100 milliseconds
    And I clear observed commands
    Then no command reaches "test.lr.light001" within 300 milliseconds

  Scenario: Fade down while held and stop on release
    Given the scripting engine is running
    And a binary sensor entity "test.remote.fadedown" named "Fade Down" class "button" with state off
    And a light entity "test.lr.light001" named "Living Room" with power off
    And a script definition "fade_down" is saved from file "fade_down.lua"
    And I start script "fade_down" with query ""
    When I subscribe to commands on "test.>"
    And the binary sensor entity "test.remote.fadedown" changes to on
    Then within 400 milliseconds command "light_step_brightness" reaches "test.lr.light001" with payload:
      """
      {"delta":-1}
      """
    And within 400 milliseconds command "light_step_brightness" reaches "test.lr.light001" with payload:
      """
      {"delta":-5}
      """
    When I clear observed commands
    And the binary sensor entity "test.remote.fadedown" changes to off
    And I wait 100 milliseconds
    And I clear observed commands
    Then no command reaches "test.lr.light001" within 300 milliseconds
