package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
)

func ListenNotifications(ctx context.Context, url string, contractToListen string) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		fmt.Print("Не удалось подключиться к WebSocket:", err)
	}
	defer conn.Close()

	// Подписка на notifications контракта auction
	subscribeMessage := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "subscribe",
		"params":  []interface{}{"notification_from_execution", map[string]string{"contract": contractToListen}},
		"id":      1,
	}

	err = conn.WriteJSON(subscribeMessage)
	if err != nil {
		fmt.Printf("Ошибка при отправке подписки: %v", err)
	}

	// Чтение уведомлений
	for {
		select {
		case <-ctx.Done():
			die(ctx.Err())
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("read:", err)
				break
			}
			var result map[string]interface{}
			err = json.Unmarshal([]byte(message), &result)
			if err != nil {
				fmt.Println("Error parsing JSON:", err)
				continue
			}

			params, ok := result["params"].([]interface{})
			if !ok || len(params) == 0 {
				continue
			}

			firstParam, ok := params[0].(map[string]interface{})
			if !ok {
				fmt.Println("Invalid param type")
				continue
			}

			state, ok := firstParam["state"].(map[string]interface{})
			if !ok {
				fmt.Println("Invalid state type")
				continue
			}

			values, ok := state["value"].([]interface{})
			if !ok || len(values) == 0 {
				fmt.Println("No values found")
				continue
			}

			// Извлекаем Base64 строку
			firstValue, ok := values[0].(map[string]interface{})
			if !ok {
				fmt.Println("Invalid value type")
				continue
			}

			base64String, ok := firstValue["value"].(string)
			if !ok {
				fmt.Println("Base64 value is missing or not a string")
				continue
			}

			// Декодируем Base64
			decodedBytes, err := base64.StdEncoding.DecodeString(base64String)
			if err != nil {
				fmt.Println("Error decoding Base64:", err)
				continue
			}
			decodedString := string(decodedBytes)
			fmt.Print("\nNOTIFICATION:", decodedString, "\n\n")
		}
	}
}
