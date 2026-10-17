package processing

import "testing"

func TestRobotFile(t *testing.T) {
	src := `*** Settings ***
Library     DoorLibrary.py
Resource    common.resource

*** Test Cases ***
Open Door Test
    [Documentation]    opens it
    [Setup]    Reset Station
    ${state}=    Get Door State
    Given Door Is Closed
    DoorLibrary.Open Door    timeout=5
    IF    $state    Log Details    x
    FOR    ${i}    IN RANGE    3
        Wait Until Open
    END

*** Keywords ***
Door Is Closed
    [Arguments]    ${door}=main
    Should Be Equal    closed    closed
`
	pf := robotFile("tests/door.robot", src)
	if !pf.IsTest || pf.Lang != "robot" {
		t.Fatalf("suite with test cases is test code: %+v", pf)
	}
	got := map[string][]string{}
	for _, s := range pf.Symbols {
		got[s.Name] = s.Callees
	}
	want := []string{"reset_station", "get_door_state", "door_is_closed", "open_door", "log_details", "wait_until_open"}
	calls := got["open_door_test"]
	if len(calls) != len(want) {
		t.Fatalf("calls of the test: got %v want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls of the test: got %v want %v", calls, want)
		}
	}
	if c := got["door_is_closed"]; len(c) != 1 || c[0] != "should_be_equal" {
		t.Errorf("keyword body: %v", c)
	}
	if pf.Spans[0].StartLine != 6 || pf.Spans[0].EndLine != 16 {
		t.Errorf("test span: %+v", pf.Spans[0])
	}
	if RobotName("Open  Door") != "open_door" || RobotName("open_door") != "open_door" {
		t.Error("Robot names must meet Python library names")
	}
	if robotFile("libs/common.resource", "*** Keywords ***\nReset Station\n    No Operation\n").IsTest {
		t.Error("a keyword library is not test code")
	}
}

func TestRobotEmbeddedArgumentKeyword(t *testing.T) {
	suite := robotFile("tests/door.robot", "*** Test Cases ***\nDoor Works\n    Open Front Door\n")
	res := robotFile("tests/door.resource", "*** Keywords ***\nOpen ${door} Door\n    Log    ${door}\n")
	files := []ParsedFile{suite, res}
	linkRobotEmbedded(files)
	if got := files[0].Symbols[0].Callees; len(got) != 1 || got[0] != res.Symbols[0].Name {
		t.Fatalf("callees %v, keyword %q", got, res.Symbols[0].Name)
	}
}
