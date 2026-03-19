Feature: Automation core lifecycle

  Scenario: Interval automation starts, updates, and stops on demand
    Given the scripting engine is running
    And a light entity "test.dev1.light001" named "Light A" with power off
    And a light entity "test.dev2.light001" named "Light B" with power off
    And a script definition "party_time" is saved from file "automation_core_interval_v1.lua"
    When I start script "party_time" with query "test.dev1.light001"
    Then at least 2 commands arrive on "test.dev1.light001" within 500 milliseconds
    When a script definition "party_time" is saved from file "automation_core_interval_v2.lua"
    And I start script "party_time" with query "test.dev2.light001"
    Then within 500 milliseconds command "light_turn_on" reaches "test.dev1.light001"
    And within 500 milliseconds command "light_turn_off" reaches "test.dev2.light001"
    When I clear observed commands
    And I stop script "party_time" with query "test.dev1.light001"
    And I stop script "party_time" with query "test.dev2.light001"
    Then no command reaches "test.dev1.light001" within 300 milliseconds
    And no command reaches "test.dev2.light001" within 300 milliseconds

  Scenario: Running automation resumes after engine restart
    Given the scripting engine is running
    And a light entity "test.dev1.light001" named "Light" with power off
    And a script definition "blink" is saved from file "automation_core_restart.lua"
    And I start script "blink" with query ""
    When the engine is restarted
    Then within 1000 milliseconds command "light_turn_on" reaches "test.dev1.light001"
