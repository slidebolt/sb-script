Feature: Lightstrip automations

  Scenario: Rainbow drift updates a virtual lightstrip frame over time
    Given the scripting engine is running
    And a lightstrip entity "test.stripa.light001" named "Strip A" with 4 segments
    And a script definition "lightstrip_rainbow" is saved from file "lightstrip_rainbow.lua"
    And I start script "lightstrip_rainbow" with query "test.stripa.>"
    When I subscribe to commands on "test.>"
    Then within 500 milliseconds command "lightstrip_set_segments" reaches "test.stripa.light001" with payload:
      """
      {
        "power": true,
        "colorMode": "rgb",
        "effect": "rainbow_drift",
        "effectSpeed": 10,
        "segments": {
          "1":{"id":1,"rgb":{"1":255,"2":255,"3":0},"brightness":180},
          "2":{"id":2,"rgb":{"1":0,"2":255,"3":0},"brightness":180},
          "3":{"id":3,"rgb":{"1":0,"2":0,"3":255},"brightness":180},
          "4":{"id":4,"rgb":{"1":128,"2":0,"3":255},"brightness":180}
        }
      }
      """
    When I clear observed commands
    Then within 500 milliseconds command "lightstrip_set_segments" reaches "test.stripa.light001" with payload:
      """
      {
        "power": true,
        "colorMode": "rgb",
        "effect": "rainbow_drift",
        "effectSpeed": 10,
        "segments": {
          "1":{"id":1,"rgb":{"1":0,"2":255,"3":0},"brightness":180},
          "2":{"id":2,"rgb":{"1":0,"2":0,"3":255},"brightness":180},
          "3":{"id":3,"rgb":{"1":128,"2":0,"3":255},"brightness":180},
          "4":{"id":4,"rgb":{"1":255,"2":0,"3":0},"brightness":180}
        }
      }
      """

  Scenario: Rainbow drift stops when the lightstrip automation is stopped
    Given the scripting engine is running
    And a lightstrip entity "test.stripa.light001" named "Strip A" with 4 segments
    And a script definition "lightstrip_rainbow" is saved from file "lightstrip_rainbow.lua"
    And I start script "lightstrip_rainbow" with query "test.stripa.>"
    When I subscribe to commands on "test.>"
    Then within 500 milliseconds command "lightstrip_set_segments" reaches "test.stripa.light001"
    When I clear observed commands
    And I stop script "lightstrip_rainbow" with query "test.stripa.>"
    Then no command reaches "test.stripa.light001" within 300 milliseconds

  Scenario: Rainbow drift keeps separate offset state per started lightstrip group
    Given the scripting engine is running
    And a lightstrip entity "test.stripa.light001" named "Strip A" with 4 segments
    And a lightstrip entity "test.stripb.light001" named "Strip B" with 4 segments
    And a script definition "lightstrip_rainbow" is saved from file "lightstrip_rainbow.lua"
    And I start script "lightstrip_rainbow" with query "test.stripa.>"
    When I subscribe to commands on "test.>"
    Then within 500 milliseconds command "lightstrip_set_segments" reaches "test.stripa.light001" with payload:
      """
      {
        "power": true,
        "colorMode": "rgb",
        "effect": "rainbow_drift",
        "effectSpeed": 10,
        "segments": {
          "1":{"id":1,"rgb":{"1":255,"2":255,"3":0},"brightness":180},
          "2":{"id":2,"rgb":{"1":0,"2":255,"3":0},"brightness":180},
          "3":{"id":3,"rgb":{"1":0,"2":0,"3":255},"brightness":180},
          "4":{"id":4,"rgb":{"1":128,"2":0,"3":255},"brightness":180}
        }
      }
      """
    When I clear observed commands
    And I start script "lightstrip_rainbow" with query "test.stripb.>"
    Then within 500 milliseconds command "lightstrip_set_segments" reaches "test.stripa.light001" with payload:
      """
      {
        "power": true,
        "colorMode": "rgb",
        "effect": "rainbow_drift",
        "effectSpeed": 10,
        "segments": {
          "1":{"id":1,"rgb":{"1":0,"2":255,"3":0},"brightness":180},
          "2":{"id":2,"rgb":{"1":0,"2":0,"3":255},"brightness":180},
          "3":{"id":3,"rgb":{"1":128,"2":0,"3":255},"brightness":180},
          "4":{"id":4,"rgb":{"1":255,"2":0,"3":0},"brightness":180}
        }
      }
      """
    And within 500 milliseconds command "lightstrip_set_segments" reaches "test.stripb.light001" with payload:
      """
      {
        "power": true,
        "colorMode": "rgb",
        "effect": "rainbow_drift",
        "effectSpeed": 10,
        "segments": {
          "1":{"id":1,"rgb":{"1":255,"2":255,"3":0},"brightness":180},
          "2":{"id":2,"rgb":{"1":0,"2":255,"3":0},"brightness":180},
          "3":{"id":3,"rgb":{"1":0,"2":0,"3":255},"brightness":180},
          "4":{"id":4,"rgb":{"1":128,"2":0,"3":255},"brightness":180}
        }
      }
      """
