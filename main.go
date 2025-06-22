package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Разрешаем подключения с любых источников
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Обновленная структура для хранения WebSocket соединений
type WebSocketManager struct {
	connections map[string]*ConnectionInfo
	mutex       sync.RWMutex
	logger      *log.Logger
}

// Структура для хранения информации о соединении
type ConnectionInfo struct {
	conn     *websocket.Conn
	lastPing time.Time
	stopChan chan bool
}

// Глобальный менеджер WebSocket соединений
var wsManager *WebSocketManager

// Структуры для сообщений
type WebSocketMessage struct {
	Command string                 `json:"command"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Data    interface{}            `json:"data,omitempty"`
	TabID   int                    `json:"tabId,omitempty"`
	ID      string                 `json:"id,omitempty"`
}

type BrowserResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Command   string      `json:"command"`
	Timestamp string      `json:"timestamp"`
}

// Типизированные структуры для аргументов инструментов
type GetHtmlArgs struct {
	TabID *int `json:"tabId,omitempty"`
}

type GetHtmlBySelectorArgs struct {
	Selector string `json:"selector"`
	TabID    *int   `json:"tabId,omitempty"`
}

type ClickElementArgs struct {
	Selector    *string                 `json:"selector,omitempty"`
	Index       *int                    `json:"index,omitempty"`
	Coordinates *map[string]interface{} `json:"coordinates,omitempty"`
	TabID       *int                    `json:"tabId,omitempty"`
}

type InputDataArgs struct {
	Selector string `json:"selector"`
	Value    string `json:"value"`
	Index    *int   `json:"index,omitempty"`
	Clear    *bool  `json:"clear,omitempty"`
	TabID    *int   `json:"tabId,omitempty"`
}

type GetConsoleLogArgs struct {
	TabID *int `json:"tabId,omitempty"`
}

type GetNetworkLogArgs struct {
	TabID *int `json:"tabId,omitempty"`
}

type SendCommandArgs struct {
	Command string                 `json:"command"`
	Data    map[string]interface{} `json:"data,omitempty"`
	TabID   *int                   `json:"tabId,omitempty"`
}

func main() {
	var transport string
	var host string
	var port string
	var wsPort string
	flag.StringVar(&transport, "t", "sse", "Transport type (stdio or sse)")
	flag.StringVar(&host, "h", "0.0.0.0", "Host of sse server")
	flag.StringVar(&wsPort, "ws_port", "8090", "Port of web socket server")
	flag.StringVar(&port, "p", "8896", "Port of sse server")
	flag.Parse()

	// Инициализируем WebSocket менеджер
	wsManager = &WebSocketManager{
		connections: make(map[string]*ConnectionInfo),
		logger:      log.New(log.Writer(), "[WebSocket] ", log.LstdFlags),
	}

	// Запускаем WebSocket сервер в отдельной горутине
	go startWebSocketServer(wsPort)

	// Создаем MCP сервер
	mcpServer := server.NewMCPServer(
		"go_mcp_browser_ext_tool",
		"1.0.0",
	)

	// Добавляем все инструменты для работы с браузером
	registerBrowserTools(mcpServer)

	log.Printf("MCP Browser Extension Tool Server запущен")
	log.Printf("WebSocket сервер: ws://localhost:%s/ws", wsPort)

	if transport == "sse" {
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL(fmt.Sprintf("http://localhost:%s", port)))
		log.Printf("MCP SSE сервер: http://127.0.0.1:%s/sse", port)
		if err := sseServer.Start(fmt.Sprintf("%s:%s", host, port)); err != nil {
			log.Fatalf("Ошибка SSE сервера: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Ошибка STDIO сервера: %v", err)
		}
	}
}

// Запуск WebSocket сервера
func startWebSocketServer(port string) {
	http.HandleFunc("/ws", handleWebSocket)

	wsManager.logger.Printf("WebSocket сервер запущен на порту %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Ошибка запуска WebSocket сервера: %v", err)
	}
}

// Обработчик WebSocket соединений
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsManager.logger.Printf("Ошибка обновления соединения: %v", err)
		return
	}
	defer conn.Close()

	clientID := fmt.Sprintf("client_%d", time.Now().UnixNano())
	wsManager.addConnection(clientID, conn)
	defer wsManager.removeConnection(clientID)

	wsManager.logger.Printf("Новое WebSocket соединение: %s", clientID)

	// Устанавливаем обработчики ping/pong
	conn.SetPingHandler(func(appData string) error {
		wsManager.logger.Printf("Получен ping от %s", clientID)
		return conn.WriteMessage(websocket.PongMessage, []byte{})
	})

	conn.SetPongHandler(func(appData string) error {
		wsManager.logger.Printf("Получен pong от %s", clientID)
		return nil
	})

	// Читаем сообщения от клиента
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wsManager.logger.Printf("Ошибка чтения сообщения от %s: %v", clientID, err)
			}
			break
		}

		// Обработка ping сообщений
		if messageType == websocket.TextMessage && string(data) == "ping" {
			wsManager.logger.Printf("Получен ping от %s", clientID)
			err = conn.WriteMessage(websocket.TextMessage, []byte("pong"))
			if err != nil {
				wsManager.logger.Printf("Ошибка отправки pong клиенту %s: %v", clientID, err)
				break
			}
			continue
		}

		// Обработка обычных JSON сообщений
		if messageType == websocket.TextMessage {
			var msg WebSocketMessage
			err := json.Unmarshal(data, &msg)
			if err != nil {
				wsManager.logger.Printf("Ошибка парсинга JSON от %s: %v", clientID, err)
				continue
			}

			wsManager.logger.Printf("Получено сообщение от %s: %+v", clientID, msg)

			// Здесь можно добавить обработку входящих сообщений от расширения
			// Например, сохранение результатов выполнения команд
		}
	}

	wsManager.logger.Printf("WebSocket соединение закрыто: %s", clientID)
}

// Методы WebSocketManager
func (wsm *WebSocketManager) addConnection(id string, conn *websocket.Conn) {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()
	wsm.connections[id] = &ConnectionInfo{
		conn:     conn,
		lastPing: time.Now(),
		stopChan: make(chan bool),
	}
}

func (wsm *WebSocketManager) removeConnection(id string) {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()
	delete(wsm.connections, id)
}

func (wsm *WebSocketManager) sendToAll(message WebSocketMessage) error {
	wsm.mutex.RLock()
	defer wsm.mutex.RUnlock()

	if len(wsm.connections) == 0 {
		return errors.New("нет активных WebSocket соединений")
	}

	var lastError error
	sentCount := 0

	for clientID, connInfo := range wsm.connections {
		err := connInfo.conn.WriteJSON(message)
		if err != nil {
			wsm.logger.Printf("Ошибка отправки сообщения клиенту %s: %v", clientID, err)
			lastError = err
		} else {
			sentCount++
			wsm.logger.Printf("Сообщение отправлено клиенту %s: %+v", clientID, message)
		}
	}

	if sentCount == 0 {
		return fmt.Errorf("не удалось отправить сообщение ни одному клиенту: %v", lastError)
	}

	return nil
}

// Регистрация всех инструментов браузера
func registerBrowserTools(mcpServer *server.MCPServer) {
	// 1. Получение HTML страницы
	getHtmlTool := mcp.NewTool("browser_get_html",
		mcp.WithDescription("Получить HTML содержимое текущей страницы браузера"),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(getHtmlTool, mcp.NewTypedToolHandler(browserGetHtmlHandler))

	// 2. Получение HTML по селектору
	getHtmlBySelectorTool := mcp.NewTool("browser_get_html_by_selector",
		mcp.WithDescription("Получить HTML элементов по CSS селектору"),
		mcp.WithString("selector",
			mcp.Required(),
			mcp.Description("CSS селектор для поиска элементов"),
		),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(getHtmlBySelectorTool, mcp.NewTypedToolHandler(browserGetHtmlBySelectorHandler))

	// 3. Клик по элементу
	clickElementTool := mcp.NewTool("browser_click_element",
		mcp.WithDescription("Кликнуть по элементу на странице"),
		mcp.WithString("selector",
			mcp.Description("CSS селектор элемента для клика"),
		),
		mcp.WithNumber("index",
			mcp.Description("Индекс элемента, если найдено несколько (по умолчанию 0)"),
		),
		mcp.WithObject("coordinates",
			mcp.Description("Координаты для клика"),
			mcp.Properties(map[string]any{
				"x": map[string]any{
					"type":        "number",
					"description": "X координата",
				},
				"y": map[string]any{
					"type":        "number",
					"description": "Y координата",
				},
			}),
		),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(clickElementTool, mcp.NewTypedToolHandler(browserClickElementHandler))

	// 4. Ввод данных
	inputDataTool := mcp.NewTool("browser_input_data",
		mcp.WithDescription("Ввести данные в поле ввода на странице"),
		mcp.WithString("selector",
			mcp.Required(),
			mcp.Description("CSS селектор поля ввода"),
		),
		mcp.WithString("value",
			mcp.Required(),
			mcp.Description("Значение для ввода"),
		),
		mcp.WithNumber("index",
			mcp.Description("Индекс элемента, если найдено несколько (по умолчанию 0)"),
		),
		mcp.WithBoolean("clear",
			mcp.Description("Очистить поле перед вводом (по умолчанию true)"),
			mcp.DefaultBool(true),
		),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(inputDataTool, mcp.NewTypedToolHandler(browserInputDataHandler))

	// 5. Получение логов консоли
	getConsoleLogTool := mcp.NewTool("browser_get_console_log",
		mcp.WithDescription("Получить логи консоли браузера"),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(getConsoleLogTool, mcp.NewTypedToolHandler(browserGetConsoleLogHandler))

	// 6. Получение network логов
	getNetworkLogTool := mcp.NewTool("browser_get_network_log",
		mcp.WithDescription("Получить network логи браузера"),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(getNetworkLogTool, mcp.NewTypedToolHandler(browserGetNetworkLogHandler))

	// 7. Отправка произвольной команды
	sendCommandTool := mcp.NewTool("browser_send_command",
		mcp.WithDescription("Отправить произвольную команду в расширение браузера"),
		mcp.WithString("command",
			mcp.Required(),
			mcp.Description("Название команды"),
		),
		mcp.WithObject("data",
			mcp.Description("Данные команды"),
		),
		mcp.WithNumber("tabId",
			mcp.Description("ID вкладки браузера (опционально)"),
		),
	)
	mcpServer.AddTool(sendCommandTool, mcp.NewTypedToolHandler(browserSendCommandHandler))
}

// Типизированные обработчики инструментов

func browserGetHtmlHandler(ctx context.Context, request mcp.CallToolRequest, args GetHtmlArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_HTML",
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_get_html", err.Error()), nil
	}

	return createSuccessResult("browser_get_html", "Команда получения HTML отправлена в браузер", params), nil
}

func browserGetHtmlBySelectorHandler(ctx context.Context, request mcp.CallToolRequest, args GetHtmlBySelectorArgs) (*mcp.CallToolResult, error) {
	params := map[string]interface{}{
		"selector": args.Selector,
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_HTML_BY_SELECTOR",
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_get_html_by_selector", err.Error()), nil
	}

	return createSuccessResult("browser_get_html_by_selector", "Команда получения HTML по селектору отправлена в браузер", params), nil
}

func browserClickElementHandler(ctx context.Context, request mcp.CallToolRequest, args ClickElementArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})

	if args.Selector != nil {
		params["selector"] = *args.Selector
	}
	if args.Index != nil {
		params["index"] = *args.Index
	}
	if args.Coordinates != nil {
		params["coordinates"] = *args.Coordinates
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	// Проверяем наличие селектора или координат
	if args.Selector == nil && args.Coordinates == nil {
		return createErrorResult("browser_click_element", "Необходимо указать 'selector' или 'coordinates'"), nil
	}

	message := WebSocketMessage{
		Command: "CLICK_ELEMENT",
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_click_element", err.Error()), nil
	}

	return createSuccessResult("browser_click_element", "Команда клика по элементу отправлена в браузер", params), nil
}

func browserInputDataHandler(ctx context.Context, request mcp.CallToolRequest, args InputDataArgs) (*mcp.CallToolResult, error) {
	params := map[string]interface{}{
		"selector": args.Selector,
		"value":    args.Value,
	}

	if args.Index != nil {
		params["index"] = *args.Index
	}
	if args.Clear != nil {
		params["clear"] = *args.Clear
	} else {
		params["clear"] = true // значение по умолчанию
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "INPUT_DATA",
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_input_data", err.Error()), nil
	}

	return createSuccessResult("browser_input_data", "Команда ввода данных отправлена в браузер", params), nil
}

func browserGetConsoleLogHandler(ctx context.Context, request mcp.CallToolRequest, args GetConsoleLogArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_CONSOLE_LOG",
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_get_console_log", err.Error()), nil
	}

	return createSuccessResult("browser_get_console_log", "Команда получения логов консоли отправлена в браузер", params), nil
}

func browserGetNetworkLogHandler(ctx context.Context, request mcp.CallToolRequest, args GetNetworkLogArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_NETWORK_LOG",
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_get_network_log", err.Error()), nil
	}

	return createSuccessResult("browser_get_network_log", "Команда получения network логов отправлена в браузер", params), nil
}

func browserSendCommandHandler(ctx context.Context, request mcp.CallToolRequest, args SendCommandArgs) (*mcp.CallToolResult, error) {
	params := map[string]interface{}{
		"command": args.Command,
	}

	if args.Data != nil {
		params["data"] = args.Data
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: args.Command,
		Params:  params,
		ID:      generateMessageID(),
	}

	err := wsManager.sendToAll(message)
	if err != nil {
		return createErrorResult("browser_send_command", err.Error()), nil
	}

	return createSuccessResult("browser_send_command", fmt.Sprintf("Команда '%s' отправлена в браузер", args.Command), params), nil
}

// Вспомогательные функции

func createSuccessResult(tool, message string, data interface{}) *mcp.CallToolResult {
	result := BrowserResponse{
		Success:   true,
		Data:      data,
		Command:   tool,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	jsonResult, _ := json.Marshal(map[string]interface{}{
		"message": message,
		"result":  result,
	})

	return mcp.NewToolResultText(string(jsonResult))
}

func createErrorResult(tool, errorMsg string) *mcp.CallToolResult {
	result := BrowserResponse{
		Success:   false,
		Error:     errorMsg,
		Command:   tool,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	jsonResult, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(jsonResult))
}

func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
