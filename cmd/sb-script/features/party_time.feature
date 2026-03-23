Feature: Party time automation

  Scenario: Party time targets RGB lights dynamically and stops cleanly
    Given the scripting engine is running
    And a light entity "test.rgb.light001" named "RGB Light" with color mode "rgb"
    And a light entity "test.white.light001" named "White Light" with color mode "color_temp"
    And a script definition "party_time" is saved from file "party_time.lua"
    When I start script "party_time" with query "test.>"
    Then within 500 milliseconds command "light_set_rgb" reaches "test.rgb.light001" with payload:
      """
      {"r":255,"g":0,"b":255}
      """
    And no command reaches "test.white.light001" within 300 milliseconds
    When I clear observed commands
    And I stop script "party_time" with query "test.>"
    Then no command reaches "test.rgb.light001" within 300 milliseconds
