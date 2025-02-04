package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	"git.frostfs.info/TrueCloudLab/hrw"
	"github.com/nspcc-dev/neo-go/pkg/core/state"
	"github.com/nspcc-dev/neo-go/pkg/core/transaction"
	"github.com/nspcc-dev/neo-go/pkg/crypto/keys"
	"github.com/nspcc-dev/neo-go/pkg/encoding/address"
	"github.com/nspcc-dev/neo-go/pkg/encoding/base58"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/actor"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/notary"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/unwrap"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"github.com/nspcc-dev/neo-go/pkg/vm/vmstate"
	"github.com/nspcc-dev/neo-go/pkg/wallet"
	"github.com/spf13/viper"
)

const (
	cfgRPCEndpoint   = "rpc_endpoint"
	cfgRPCEndpointWC = "rpc_endpoint_ws"
	cfgBackendKey    = "backend_key"
	cfgWallet        = "wallet"
	cfgPassword      = "password"
	cfgNnsContract   = "nns_contract"
	cfgBackendURL    = "backend_url"
)

var listOfTickets []string

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM) // если пользователь нажмет ctrl+C, то завершим выполнение

	if len(os.Args) != 2 { // go run client/main.go client/config.yml - команда запуска, проверяем, что параметров 2
		die(fmt.Errorf("invalid args: %v", os.Args))
	}

	viper.GetViper().SetConfigType("yml") // конфиг написан в формате yaml

	configFile, err := os.Open(os.Args[1]) // открываем конфиг
	die(err)
	die(viper.GetViper().ReadConfig(configFile)) // считываем
	die(configFile.Close())                      // закрываем

	rpcCli, err := rpcclient.New(ctx, viper.GetString(cfgRPCEndpoint), rpcclient.Options{}) // создание rpc клиента взаимодействия приложений
	// или пользователей с нодой bc, rpc_endpoint = "http://localhost:30333"
	die(err)

	rpcEndpointWc := viper.GetString(cfgRPCEndpointWC)

	backendKey, err := keys.NewPublicKeyFromString(viper.GetString(cfgBackendKey)) // получаем PK backendа, у него есть кошелек
	die(err)

	w, err := wallet.NewWalletFromFile(viper.GetString(cfgWallet)) //  получаем кошелек пользователя (на нем не будет денег, т.к за него будет платить
	// backend, но на нем будут nft)
	die(err)
	acc := w.GetAccount(w.GetChangeAddress())                 // получаем аккаунт из кошелька (акк там один, но бывает и много, как в wallet1 ex)
	err = acc.Decrypt(viper.GetString(cfgPassword), w.Scrypt) // подтверждаем его паролем
	die(err)

	nnsContractHash := viper.GetString(cfgNnsContract)

	nftContractHash, err := GetNnsResolve("nft.auc", nnsContractHash, viper.GetString(cfgRPCEndpoint))
	die(err)
	auctionContractHash, err := GetNnsResolve("auc.auc", nnsContractHash, viper.GetString(cfgRPCEndpoint))
	die(err)

	numbers := make([]int, 100) // создание списка имен пока еще свободных nft
	for i := 1; i <= 100; i++ {
		numbers[i-1] = i
	}
	listOfTickets = make([]string, len(numbers))
	for i, num := range numbers {
		listOfTickets[i] = strconv.Itoa(num)
	}

	go ListenNotifications(ctx, rpcEndpointWc, auctionContractHash.StringLE())

	in := make(chan string)

	go func(ctx context.Context, in chan<- string) {
		reader := bufio.NewReader(os.Stdin)
		for {
			select {
			case <-ctx.Done():
				close(in)
				die(ctx.Err())
				return
			default:
				input, err := reader.ReadString('\n')
				if err != nil {
					fmt.Println("Ошибка ввода:", err)
					continue
				}
				in <- strings.TrimSpace(input)
			}
		}
	}(ctx, in)

	for {
		fmt.Print("Введите команду: ")

		select {
		case <-ctx.Done():
			die(ctx.Err())
		case input := <-in:
			args := strings.Fields(input)

			commandName := args[0]

			die(claimNotaryDeposit(acc)) // запрос НД

			switch commandName {
			case "startAuction":
				nftId := args[1] // lot

				initBetStr := args[2] // initBet
				initBet, err := strconv.Atoi(initBetStr)
				if err != nil {
					fmt.Printf("Error converting bet number to integer: %v\n", err)
					return
				}
				die(makeNotaryRequestStartAuction(backendKey, acc, rpcCli, auctionContractHash, nftId, initBet)) // создание НЗ (оборачивает main tx, которая состоит в вызове метода контракта)
			case "getNFT":
				die(makeNotaryRequestGetNft(backendKey, acc, rpcCli, nftContractHash))
			case "makeBet":
				betStr := args[1]
				bet, err := strconv.Atoi(betStr)
				if err != nil {
					fmt.Printf("Error converting bet number to integer: %v\n", err)
					return
				}
				die(makeNotaryRequestMakeBet(backendKey, acc, rpcCli, auctionContractHash, bet))
			case "finishAuction":
				die(makeNotaryRequestFinishAuction(backendKey, acc, rpcCli, auctionContractHash))
			default:
				fmt.Printf("Unknown commandName: %s\n", commandName)
			}
		}
	}
}

func GetNnsResolve(domainName string, nnsContractHash string, rpcEndpoint string) (util.Uint160, error) {

	type StackItem struct {
		Type  string `json:"type"`
		Value []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"value"`
	}

	type RPCResponse struct {
		ID      int    `json:"id"`
		JSONRPC string `json:"jsonrpc"`
		Result  struct {
			State         string        `json:"state"`
			GasConsumed   string        `json:"gasconsumed"`
			Script        string        `json:"script"`
			Stack         []StackItem   `json:"stack"`
			Exception     interface{}   `json:"exception"`
			Notifications []interface{} `json:"notifications"`
		} `json:"result"`
	}

	rpcRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "invokefunction",
		"params": []interface{}{
			nnsContractHash,
			"resolve",
			[]interface{}{
				map[string]interface{}{
					"type":  "String",
					"value": domainName,
				},
				map[string]interface{}{
					"type":  "Integer",
					"value": "16",
				},
			},
		},
		"id": 1,
	}

	jsonData, err := json.Marshal(rpcRequest)
	if err != nil {
		fmt.Printf("Ошибка кодирования JSON: %v\n", err)
		return util.Uint160{}, err
	}

	// Отправка HTTP POST-запроса
	resp, err := http.Post(rpcEndpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Ошибка отправки запроса: %v\n", err)
		return util.Uint160{}, err
	}
	defer resp.Body.Close()

	// Чтение ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Ошибка чтения ответа: %v\n", err)
		return util.Uint160{}, err
	}

	var rpcResponse RPCResponse

	// Парсим JSON-ответ
	err = json.Unmarshal(body, &rpcResponse)
	if err != nil {
		fmt.Println("Ошибка парсинга JSON:", err)
		return util.Uint160{}, err
	}

	// Извлекаем нужное значение из стека
	if len(rpcResponse.Result.Stack) > 0 && len(rpcResponse.Result.Stack[0].Value) > 0 {
		value := rpcResponse.Result.Stack[0].Value[0].Value
		decoded64, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			fmt.Println("Ошибка при декодировании Base64:", err)
		}

		decoded58, err := base58.CheckDecode(string(decoded64))
		if err != nil {
			return util.Uint160{}, err
		}
		contractHashStr := hex.EncodeToString(decoded58)[2:]
		contractHash, err := util.Uint160DecodeStringBE(contractHashStr)
		if err != nil {
			return util.Uint160{}, err
		}

		return contractHash, nil
	}

	return util.Uint160{}, fmt.Errorf("stack is empty or unexpected structure")
}

func claimNotaryDeposit(acc *wallet.Account) error {
	resp, err := http.Get(viper.GetString(cfgBackendURL) + "/notary-deposit/" + acc.Address) // формируем http запрос к backendу, он слушает http
	// запросы на порту 5555, туда и говорим о своей просьбу накинуть нам НД
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notary deposit failed: %d, %s", resp.StatusCode, resp.Status)
	}

	return nil
}
func makeNotaryRequestPreProcessing(acc *wallet.Account, backendKey *keys.PublicKey, rpcCli *rpcclient.Client) (*notary.Actor, error) {
	coSigners := []actor.SignerAccount{
		{
			Signer: transaction.Signer{ // первый подписант - backend, который будет платить за tx, когда она примется (потому что платит первый подписант). Мы не знаем его  SK, поэтому ставим PK
				Account: backendKey.GetScriptHash(),
				Scopes:  transaction.None,
			},
			Account: notary.FakeSimpleAccount(backendKey),
		},
		{
			Signer: transaction.Signer{
				Account: acc.ScriptHash(), // следующий подписант - client, данная программа, она знает свой SK, поэтому ставит его
				Scopes:  transaction.Global,
			},
			Account: acc,
		},
	}

	nAct, err := notary.NewActor(rpcCli, coSigners, acc) // обертка актора (клиенты; подписанты; акк, который отправляет tx) для создания НЗ
	if err != nil {
		return nil, err
	}

	return nAct, err
}

func makeNotaryRequestPostProcessing(tx *transaction.Transaction, nAct *notary.Actor) (*state.AppExecResult, error) {
	mainHash, fallbackHash, vub, err := nAct.Notarize(tx, nil) // отправка нотариального запроса; vub = valid until block
	if err != nil {
		return nil, err
	}

	fmt.Printf("Notarize sending: mainHash - %v, fallbackHash - %v, vub - %d\n", mainHash, fallbackHash, vub)

	res, err := nAct.Wait(mainHash, fallbackHash, vub, err) // ждем пока примется какя-нибудь tx  (основная (main), если все хорошо, либо fallBack)
	if err != nil {
		return nil, err
	}

	if res.VMState != vmstate.Halt {
		return nil, fmt.Errorf("invalid vm state: %s", res.VMState)
	}

	return res, err
}

func makeNotaryRequestGetNft(backendKey *keys.PublicKey, acc *wallet.Account, rpcCli *rpcclient.Client, contractHash util.Uint160) error {
	nftName, err := getFreeTicket(rpcCli, acc, contractHash) // находит свободную гифку
	if err != nil {
		return fmt.Errorf("get free ticket: %w", err)
	}

	nAct, err := makeNotaryRequestPreProcessing(acc, backendKey, rpcCli)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPreProcessing: %w", err)
	}

	tx, err := nAct.MakeTunedCall(contractHash, "mint", nil, nil, acc.ScriptHash(), nftName) // tx = вызов метода mint на
	// контракте nft - себе получаем json
	if err != nil {
		return err
	}

	res, err := makeNotaryRequestPostProcessing(tx, nAct)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPostProcessing: %w", err)
	}

	if len(res.Stack) != 1 {
		return fmt.Errorf("invalid stack size: %d", len(res.Stack))
	}
	tokenID, err := res.Stack[0].TryBytes() // если все хорошо, значит токен создан, берем его со стека
	if err != nil {
		return err
	}

	fmt.Println("new token id", hex.EncodeToString(tokenID))

	return nil
}

func makeNotaryRequestStartAuction(backendKey *keys.PublicKey, acc *wallet.Account, rpcCli *rpcclient.Client, contractAuctionHash util.Uint160, nftId string, initBet int) error {
	nAct, err := makeNotaryRequestPreProcessing(acc, backendKey, rpcCli)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPreProcessing: %w", err)
	}

	nftIdBytes, err := hex.DecodeString(nftId)
	if err != nil {
		fmt.Printf("Invalid convertion nftId: %s", err)
	}
	tx, err := nAct.MakeTunedCall(contractAuctionHash, "start", nil, nil, acc.ScriptHash(), nftIdBytes, initBet) // tx = вызов метода start на
	// контракте auction
	if err != nil {
		return err
	}

	_, err = makeNotaryRequestPostProcessing(tx, nAct)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPostProcessing: %w", err)
	}

	return nil
}

func makeNotaryRequestMakeBet(backendKey *keys.PublicKey, acc *wallet.Account, rpcCli *rpcclient.Client, contractHash util.Uint160, bet int) error {

	nAct, err := makeNotaryRequestPreProcessing(acc, backendKey, rpcCli)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPreProcessing: %w", err)
	}

	tx, err := nAct.MakeTunedCall(contractHash, "makeBet", nil, nil, acc.ScriptHash(), bet)
	if err != nil {
		return fmt.Errorf("failed to create transaction for makeBet: %w", err)
	}

	_, err = makeNotaryRequestPostProcessing(tx, nAct)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPostProcessing: %w", err)
	}

	return nil
}

func makeNotaryRequestFinishAuction(backendKey *keys.PublicKey, acc *wallet.Account, rpcCli *rpcclient.Client, contractHash util.Uint160) error {
	nAct, err := makeNotaryRequestPreProcessing(acc, backendKey, rpcCli)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPreProcessing: %w", err)
	}

	tx, err := nAct.MakeTunedCall(contractHash, "finish", nil, nil, acc.ScriptHash()) // tx = вызов метода finish на контракте auction
	if err != nil {
		return err
	}

	res, err := makeNotaryRequestPostProcessing(tx, nAct)
	if err != nil {
		return fmt.Errorf("makeNotaryRequestPostProcessing: %w", err)
	}

	if len(res.Stack) != 1 {
		return fmt.Errorf("invalid stack size: %d", len(res.Stack))
	}

	winnerBytes, ok := res.Stack[0].Value().([]byte)
	if !ok {
		panic("Stack[0] value is not of type []byte")
	}

	winner, _ := util.Uint160DecodeBytesBE(winnerBytes)

	fmt.Printf("auction finished winner %s\n", address.Uint160ToString(winner))

	return nil
}

func getFreeTicket(cli *rpcclient.Client, acc *wallet.Account, contractHash util.Uint160) (string, error) {
	// пробегает по списку гифок, определяет свободна или нет, дергая ownerOf. Найдя первую свободную, возвращает

	indexes := make([]uint64, len(listOfTickets))
	for i := range indexes {
		indexes[i] = uint64(i)
	}

	act, err := actor.NewSimple(cli, acc)
	if err != nil {
		return "", err
	}

	h := hrw.Hash(acc.ScriptHash().BytesBE()) // сортировка опциональна, может быть какая-то другая логика поиска нужной гифки (ex ML), это просто
	// как пример того, в каком порядке можно обходить список всех возможных гифок в поиске свободной
	// если каждый клиент пойдет по порядку, все начнут с начала, а свободны только последние 2, то они все пройдут весь список - очень неэффективно, пусть
	// идут с разных концов, используем рандеву-хэширование
	hrw.Sort(indexes, h)

	var ticket string
	for _, index := range indexes {
		ticket = listOfTickets[index]

		hash := sha256.New()
		hash.Write([]byte(ticket))
		tokenID := hash.Sum(nil)

		if _, err := unwrap.Uint160(act.Call(contractHash, "ownerOf", tokenID)); err != nil {
			break
		}
	}

	if ticket == "" {
		return "", errors.New("all tickets are taken") // не осталось свободных токенов
	}

	return ticket, nil
}

func die(err error) {
	if err == nil {
		return
	}

	debug.PrintStack()
	_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
