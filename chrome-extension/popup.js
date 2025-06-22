// JavaScript для popup окна расширения

document.addEventListener('DOMContentLoaded', function() {
    const connectionStatus = document.getElementById('connectionStatus');
    const connectBtn = document.getElementById('connectBtn');
    const openDevToolsBtn = document.getElementById('openDevToolsBtn');
    
    let isConnected = false;

    // Проверяем статус подключения при открытии popup
    checkConnectionStatus();

    // Обработчик кнопки подключения
    connectBtn.addEventListener('click', function() {
        if (isConnected) {
            disconnect();
        } else {
            connect();
        }
    });

    // Обработчик кнопки открытия DevTools
    openDevToolsBtn.addEventListener('click', function() {
        // Инструкция пользователю
        alert('Для использования полного функционала:\n\n1. Откройте DevTools (F12)\n2. Перейдите на вкладку "Browser Tool"\n3. Используйте панель для выполнения команд');
    });

    function checkConnectionStatus() {
        // Отправляем сообщение background script для проверки статуса
        chrome.runtime.sendMessage({
            type: 'CHECK_CONNECTION_STATUS'
        }, function(response) {
            if (response && response.connected) {
                updateConnectionStatus(true);
            } else {
                updateConnectionStatus(false);
            }
        });
    }

    function connect() {
        connectBtn.disabled = true;
        connectBtn.textContent = 'Подключение...';
        
        chrome.runtime.sendMessage({
            type: 'CONNECT_WEBSOCKET'
        }, function(response) {
            connectBtn.disabled = false;
            
            if (response && response.success) {
                updateConnectionStatus(true);
                showNotification('Успешно подключено к серверу', 'success');
            } else {
                updateConnectionStatus(false);
                showNotification('Ошибка подключения к серверу', 'error');
            }
        });
    }

    function disconnect() {
        connectBtn.disabled = true;
        connectBtn.textContent = 'Отключение...';
        
        chrome.runtime.sendMessage({
            type: 'DISCONNECT_WEBSOCKET'
        }, function(response) {
            connectBtn.disabled = false;
            updateConnectionStatus(false);
            showNotification('Отключено от сервера', 'info');
        });
    }

    function updateConnectionStatus(connected) {
        isConnected = connected;
        
        if (connected) {
            connectionStatus.textContent = 'Подключено';
            connectionStatus.className = 'status status-connected';
            connectBtn.textContent = 'Отключиться';
        } else {
            connectionStatus.textContent = 'Отключено';
            connectionStatus.className = 'status status-disconnected';
            connectBtn.textContent = 'Подключиться';
        }
    }

    function showNotification(message, type) {
        // Создаем временное уведомление
        const notification = document.createElement('div');
        notification.style.cssText = `
            position: fixed;
            top: 10px;
            left: 50%;
            transform: translateX(-50%);
            padding: 8px 12px;
            border-radius: 4px;
            font-size: 12px;
            font-weight: 500;
            z-index: 1000;
            transition: opacity 0.3s ease;
        `;
        
        switch (type) {
            case 'success':
                notification.style.backgroundColor = '#d4edda';
                notification.style.color = '#155724';
                notification.style.border = '1px solid #c3e6cb';
                break;
            case 'error':
                notification.style.backgroundColor = '#f8d7da';
                notification.style.color = '#721c24';
                notification.style.border = '1px solid #f5c6cb';
                break;
            case 'info':
                notification.style.backgroundColor = '#d1ecf1';
                notification.style.color = '#0c5460';
                notification.style.border = '1px solid #bee5eb';
                break;
        }
        
        notification.textContent = message;
        document.body.appendChild(notification);
        
        // Удаляем уведомление через 3 секунды
        setTimeout(() => {
            notification.style.opacity = '0';
            setTimeout(() => {
                if (notification.parentNode) {
                    notification.parentNode.removeChild(notification);
                }
            }, 300);
        }, 3000);
    }

    // Слушаем сообщения от background script
    chrome.runtime.onMessage.addListener(function(message, sender, sendResponse) {
        if (message.type === 'CONNECTION_STATUS_CHANGED') {
            updateConnectionStatus(message.connected);
        }
    });
});