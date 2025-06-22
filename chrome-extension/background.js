// Background script для расширения Chrome
class WebSocketManager {
  constructor() {
    this.ws = null;
    this.isConnected = false;
    this.reconnectAttempts = 0;
    this.maxReconnectAttempts = 5;
    this.reconnectDelay = 1000;
    this.serverUrl = 'ws://localhost:8090/ws';
    this.messageQueue = [];
  }

  connect() {
    try {
      this.ws = new WebSocket(this.serverUrl);
      
      this.ws.onopen = () => {
        console.log('WebSocket соединение установлено');
        this.isConnected = true;
        this.reconnectAttempts = 0;
        this.processMessageQueue();
      };

      this.ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
          this.handleMessage(message);
        } catch (error) {
          console.error('Ошибка парсинга сообщения:', error);
        }
      };

      this.ws.onclose = () => {
        console.log('WebSocket соединение закрыто');
        this.isConnected = false;
        this.attemptReconnect();
      };

      this.ws.onerror = (error) => {
        console.error('Ошибка WebSocket:', error);
        this.isConnected = false;
      };
    } catch (error) {
      console.error('Ошибка создания WebSocket соединения:', error);
      this.attemptReconnect();
    }
  }

  attemptReconnect() {
    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      this.reconnectAttempts++;
      console.log(`Попытка переподключения ${this.reconnectAttempts}/${this.maxReconnectAttempts}`);
      setTimeout(() => {
        this.connect();
      }, this.reconnectDelay * this.reconnectAttempts);
    } else {
      console.error('Превышено максимальное количество попыток переподключения');
    }
  }

  sendMessage(message) {
    if (this.isConnected && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    } else {
      console.log('WebSocket не подключен, добавляем сообщение в очередь');
      this.messageQueue.push(message);
      if (!this.isConnected) {
        this.connect();
      }
    }
  }

  processMessageQueue() {
    while (this.messageQueue.length > 0) {
      const message = this.messageQueue.shift();
      this.sendMessage(message);
    }
  }

  handleMessage(message) {
    console.log('Получено сообщение от сервера:', message);
    
    // Отправляем сообщение в DevTools панель
    chrome.runtime.sendMessage({
      type: 'SERVER_RESPONSE',
      data: message
    }).catch(error => {
      console.log('DevTools панель не активна:', error);
    });
  }

  disconnect() {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
      this.isConnected = false;
    }
  }
}

// Создаем экземпляр WebSocket менеджера
const wsManager = new WebSocketManager();

// Обработчик сообщений от content script и DevTools панели
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  console.log('Получено сообщение:', message);

  switch (message.type) {
    case 'CONNECT_WEBSOCKET':
      wsManager.connect();
      sendResponse({ success: true });
      break;

    case 'DISCONNECT_WEBSOCKET':
      wsManager.disconnect();
      sendResponse({ success: true });
      break;

    case 'SEND_COMMAND':
      wsManager.sendMessage({
        command: message.command,
        params: message.params,
        tabId: sender.tab?.id
      });
      sendResponse({ success: true });
      break;

    case 'GET_HTML':
      // Отправляем команду в content script
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'GET_HTML',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'get_html',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'GET_HTML_BY_SELECTOR':
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'GET_HTML_BY_SELECTOR',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'get_html_by_selector',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'CLICK_ELEMENT':
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'CLICK_ELEMENT',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'click_element',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'INPUT_DATA':
      if (sender.tab) {
        chrome.tabs.sendMessage(sender.tab.id, {
          type: 'INPUT_DATA',
          params: message.params
        }).then(response => {
          wsManager.sendMessage({
            command: 'input_data',
            params: message.params,
            data: response,
            tabId: sender.tab.id
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'GET_CONSOLE_LOG':
      // Для получения логов консоли используем debugger API
      if (sender.tab) {
        chrome.debugger.attach({ tabId: sender.tab.id }, "1.0", () => {
          chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Runtime.enable", {}, () => {
            chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Log.enable", {}, () => {
              // Получаем логи консоли
              chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Runtime.getConsoleAPICalls", {}, (result) => {
                wsManager.sendMessage({
                  command: 'get_console_log',
                  params: message.params,
                  data: result,
                  tabId: sender.tab.id
                });
                chrome.debugger.detach({ tabId: sender.tab.id });
              });
            });
          });
        });
      }
      sendResponse({ success: true });
      break;

    case 'GET_NETWORK_LOG':
      // Для получения network логов используем debugger API
      if (sender.tab) {
        chrome.debugger.attach({ tabId: sender.tab.id }, "1.0", () => {
          chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Network.enable", {}, () => {
            // Получаем network логи
            chrome.debugger.sendCommand({ tabId: sender.tab.id }, "Network.getResponseBody", {}, (result) => {
              wsManager.sendMessage({
                command: 'get_network_log',
                params: message.params,
                data: result,
                tabId: sender.tab.id
              });
              chrome.debugger.detach({ tabId: sender.tab.id });
            });
          });
        });
      }
      sendResponse({ success: true });
      break;

    default:
      console.log('Неизвестный тип сообщения:', message.type);
      sendResponse({ success: false, error: 'Unknown message type' });
  }

  return true; // Указывает, что ответ будет отправлен асинхронно
});

// Автоматическое подключение при запуске расширения
chrome.runtime.onStartup.addListener(() => {
  wsManager.connect();
});

chrome.runtime.onInstalled.addListener(() => {
  wsManager.connect();
});