package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestHistorySearchRoutesAndAuditsWithoutQueryOrURL(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	limit := 10
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSearchHistoryHandler(
			context.Background(), mcp.CallToolRequest{},
			historySearchArgs{
				personalPageArgs: personalPageArgs{
					personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"},
					Limit:                &limit,
				},
				Text: "caller-secret-query",
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	select {
	case leaked := <-otherConnection.messages:
		t.Fatalf("history request leaked to another browser: %#v", leaked)
	default:
	}
	if request.Command != protocol.CommandHistorySearch || request.Target != nil {
		t.Fatalf("history request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil || params["text"] != "caller-secret-query" || params["limit"] != float64(limit) {
		t.Fatalf("history params = %#v, error = %v", params, err)
	}
	response := historyWireResult{
		Kind: "history", Items: []historyWireItem{{
			ID: "1", URL: "https://example.com/private?token=%5BREDACTED%5D", Title: "Private",
			LastVisitTime: 10, VisitCount: 2, TypedCount: 1,
		}}, TotalMatched: 1, Warnings: []string{},
	}
	respondToToolRequest(t, service, connection, request, response)
	result := <-resultChannel
	text := toolText(t, result)
	if result.IsError || strings.Contains(text, "browser-secret") || strings.Contains(audit.String(), "caller-secret-query") || strings.Contains(audit.String(), "example.com") {
		t.Fatalf("history data leaked: result=%s audit=%s", text, audit.String())
	}
}

func TestPersonalDataDestructiveActionsRequireConfirmationBeforeDispatch(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	for name, invoke := range map[string]func() *mcp.CallToolResult{
		"delete url": func() *mcp.CallToolResult {
			result, _ := service.browserDeleteHistoryURLHandler(context.Background(), mcp.CallToolRequest{}, confirmedURLArgs{
				personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, URL: "https://example.com/",
			})
			return result
		},
		"delete range": func() *mcp.CallToolResult {
			result, _ := service.browserDeleteHistoryRangeHandler(context.Background(), mcp.CallToolRequest{}, historyRangeArgs{
				personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, StartTime: 1, EndTime: 2,
			})
			return result
		},
		"clear history": func() *mcp.CallToolResult {
			result, _ := service.browserClearHistoryHandler(context.Background(), mcp.CallToolRequest{}, confirmedPersonalArgs{
				personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"},
			})
			return result
		},
		"remove tree": func() *mcp.CallToolResult {
			result, _ := service.browserRemoveBookmarkHandler(context.Background(), mcp.CallToolRequest{}, bookmarkRemoveArgs{
				personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, BookmarkID: "folder", Recursive: true,
			})
			return result
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := invoke()
			if result == nil || !result.IsError || decodeToolResponse(t, result).Error.Code != protocol.CodeConfirmationRequired {
				t.Fatalf("result = %#v", result)
			}
		})
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("unconfirmed mutation reached browser: %#v", message)
	default:
	}
}

func TestBookmarkAndReadingListMutationsUseDedicatedCommands(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		title := "Updated"
		result, _ := service.browserUpdateBookmarkHandler(context.Background(), mcp.CallToolRequest{}, bookmarkUpdateArgs{
			personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, BookmarkID: "42", Title: &title,
		})
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandBookmarksUpdate {
		t.Fatalf("bookmark command = %q", request.Command)
	}
	bookmark := bookmarkWireItem{ID: "42", ParentID: "1", Title: "Updated", URL: "https://example.com/", DateAdded: 1}
	respondToToolRequest(t, service, connection, request, bookmarkWireResult{
		Kind: "bookmark_mutation", Bookmarks: []bookmarkWireItem{bookmark}, TotalMatched: 1,
		BookmarkID: "42", Operation: "update", Changed: true, RemovedIDs: []string{}, Warnings: []string{},
	})
	if result := <-resultChannel; result.IsError || !strings.Contains(toolText(t, result), `"bookmarkId":"42"`) {
		t.Fatalf("bookmark result = %s", toolText(t, result))
	}

	resultChannel = make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserAddReadingListHandler(context.Background(), mcp.CallToolRequest{}, readingListAddArgs{
			personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"},
			URL:                  "https://example.com/article", Title: "Article",
		})
		resultChannel <- result
	}()
	request = receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandReadingListAdd {
		t.Fatalf("reading-list command = %q", request.Command)
	}
	entry := readingListWireEntry{URL: "https://example.com/article", Title: "Article", CreationTime: 1, LastUpdateTime: 1}
	respondToToolRequest(t, service, connection, request, readingListWireResult{
		Kind: "reading_list_mutation", Entries: []readingListWireEntry{entry}, TotalMatched: 1,
		Operation: "add", Changed: true, TargetURL: entry.URL, Warnings: []string{},
	})
	if result := <-resultChannel; result.IsError {
		t.Fatalf("reading-list result = %s", toolText(t, result))
	}
}

func TestRemainingPersonalDataHandlersRouteTypedResults(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	limit := 25
	completePersonalRequest(t, service, connection, protocol.CommandHistoryGetVisits, func() (*mcp.CallToolResult, error) {
		return service.browserGetHistoryVisitsHandler(context.Background(), mcp.CallToolRequest{}, historyVisitsArgs{
			personalPageArgs: personalPageArgs{personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, Limit: &limit},
			URL:              "https://example.com/",
		})
	}, historyWireResult{
		Kind: "visits", Visits: []historyWireVisit{{
			ID: "1", VisitID: "11", VisitTime: 1, Transition: "link",
		}}, TotalMatched: 1, Warnings: []string{},
	})

	bookmark := bookmarkWireItem{ID: "42", ParentID: "1", Title: "Example", URL: "https://example.com/", DateAdded: 1}
	completePersonalRequest(t, service, connection, protocol.CommandBookmarksList, func() (*mcp.CallToolResult, error) {
		return service.browserListBookmarksHandler(context.Background(), mcp.CallToolRequest{}, bookmarkListArgs{
			personalPageArgs: personalPageArgs{personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, Limit: &limit},
			ParentID:         "1",
		})
	}, bookmarkWireResult{Kind: "bookmarks", Bookmarks: []bookmarkWireItem{bookmark}, TotalMatched: 1, RemovedIDs: []string{}, Warnings: []string{}})

	completePersonalRequest(t, service, connection, protocol.CommandBookmarksCreate, func() (*mcp.CallToolResult, error) {
		return service.browserCreateBookmarkHandler(context.Background(), mcp.CallToolRequest{}, bookmarkCreateArgs{
			personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"},
			Title:                "Example", URL: "https://example.com/", ParentID: "1",
		})
	}, bookmarkWireResult{
		Kind: "bookmark_mutation", Bookmarks: []bookmarkWireItem{bookmark}, TotalMatched: 1,
		BookmarkID: "42", Operation: "create", Changed: true, RemovedIDs: []string{}, Warnings: []string{},
	})

	completePersonalRequest(t, service, connection, protocol.CommandBookmarksMove, func() (*mcp.CallToolResult, error) {
		return service.browserMoveBookmarkHandler(context.Background(), mcp.CallToolRequest{}, bookmarkMoveArgs{
			personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"},
			BookmarkID:           "42", ParentID: "1",
		})
	}, bookmarkWireResult{
		Kind: "bookmark_mutation", Bookmarks: []bookmarkWireItem{bookmark}, TotalMatched: 1,
		BookmarkID: "42", Operation: "move", Changed: true, RemovedIDs: []string{}, Warnings: []string{},
	})

	entry := readingListWireEntry{URL: "https://example.com/article", Title: "Article", CreationTime: 1, LastUpdateTime: 2}
	completePersonalRequest(t, service, connection, protocol.CommandReadingListList, func() (*mcp.CallToolResult, error) {
		return service.browserListReadingListHandler(context.Background(), mcp.CallToolRequest{}, readingListArgs{
			personalPageArgs: personalPageArgs{personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, Limit: &limit},
			HasBeenRead:      boolPointer(false),
		})
	}, readingListWireResult{Kind: "reading_list", Entries: []readingListWireEntry{entry}, TotalMatched: 1, Warnings: []string{}})

	completePersonalRequest(t, service, connection, protocol.CommandReadingListUpdate, func() (*mcp.CallToolResult, error) {
		return service.browserUpdateReadingListHandler(context.Background(), mcp.CallToolRequest{}, readingListUpdateArgs{
			personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"},
			URL:                  "https://example.com/article", HasBeenRead: boolPointer(true),
		})
	}, readingListWireResult{
		Kind: "reading_list_mutation", Entries: []readingListWireEntry{entry}, TotalMatched: 1,
		Operation: "update", Changed: true, TargetURL: entry.URL, Warnings: []string{},
	})

	completePersonalRequest(t, service, connection, protocol.CommandReadingListRemove, func() (*mcp.CallToolResult, error) {
		return service.browserRemoveReadingListHandler(context.Background(), mcp.CallToolRequest{}, readingListRemoveArgs{
			personalDataBaseArgs: personalDataBaseArgs{BrowserID: "browser-a"}, URL: "https://example.com/article",
		})
	}, readingListWireResult{
		Kind: "reading_list_mutation", Operation: "remove", Changed: true,
		TargetURL: entry.URL, Warnings: []string{},
	})
}

func TestPersonalDataDecoderAndGenericBoundaryFailClosed(t *testing.T) {
	t.Parallel()

	valid := bookmarkWireResult{
		Kind: "bookmarks", Bookmarks: []bookmarkWireItem{{
			ID: "42", ParentID: "1", Title: "Example", URL: "https://example.com/", DateAdded: 1,
		}}, TotalMatched: 1, RemovedIDs: []string{}, Warnings: []string{},
	}
	if _, _, err := decodePersonalDataResult(mustJSON(t, valid), protocol.CommandBookmarksList); err != nil {
		t.Fatalf("decodePersonalDataResult(valid) error = %v", err)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`null`),
		json.RawMessage(`{"kind":"bookmarks","bookmarks":[],"totalMatched":0,"nextCursor":"","bookmarkId":"","operation":"","changed":false,"removedIds":[],"warnings":[],"extra":true}`),
		mustJSON(t, bookmarkWireResult{Kind: "bookmarks", TotalMatched: maxPersonalDataScan + 1, RemovedIDs: []string{}, Warnings: []string{}}),
	} {
		if _, _, err := decodePersonalDataResult(raw, protocol.CommandBookmarksList); err == nil {
			t.Fatalf("decodePersonalDataResult(%s) error = nil", raw)
		}
	}
	unsafeHistory := historyWireResult{Kind: "history", Items: []historyWireItem{{
		ID: "1", URL: "https://example.com/?token=secret", Title: "Unsafe",
	}}, TotalMatched: 1, Warnings: []string{}}
	if _, _, err := decodePersonalDataResult(mustJSON(t, unsafeHistory), protocol.CommandHistorySearch); err == nil {
		t.Fatal("an unredacted sensitive history URL passed server validation")
	}

	service, connection, _ := newTestService(t)
	for _, command := range []string{
		protocol.CommandHistorySearch, protocol.CommandHistoryGetVisits, protocol.CommandHistoryDeleteURL,
		protocol.CommandHistoryDeleteRange, protocol.CommandHistoryDeleteAll, protocol.CommandBookmarksList,
		protocol.CommandBookmarksCreate, protocol.CommandBookmarksUpdate, protocol.CommandBookmarksMove,
		protocol.CommandBookmarksRemove, protocol.CommandReadingListList, protocol.CommandReadingListAdd,
		protocol.CommandReadingListUpdate, protocol.CommandReadingListRemove,
	} {
		result, err := service.browserSendCommandHandler(context.Background(), mcp.CallToolRequest{}, sendCommandArgs{BrowserID: "browser-a", Command: command})
		if err != nil || result == nil || !result.IsError || decodeToolResponse(t, result).Error.Code != protocol.CodeInvalidCommand {
			t.Fatalf("browserSendCommandHandler(%q) = (%#v, %v)", command, result, err)
		}
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("generic personal-data command reached browser: %#v", message)
	default:
	}
}

func completePersonalRequest(
	t *testing.T,
	service *Service,
	connection *toolTestConnection,
	command string,
	invoke func() (*mcp.CallToolResult, error),
	response any,
) {
	t.Helper()
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := invoke()
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	if request.Command != command || request.Target != nil {
		t.Fatalf("personal-data request = %#v, want command %q", request, command)
	}
	respondToToolRequest(t, service, connection, request, response)
	if result := <-resultChannel; result == nil || result.IsError {
		t.Fatalf("personal-data result = %#v", result)
	}
}

func boolPointer(value bool) *bool { return &value }
