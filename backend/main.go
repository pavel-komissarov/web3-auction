package main

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	morphclient "git.frostfs.info/TrueCloudLab/frostfs-node/pkg/morph/client"
	"git.frostfs.info/TrueCloudLab/frostfs-node/pkg/morph/subscriber"
	"git.frostfs.info/TrueCloudLab/frostfs-node/pkg/util/logger"
	cid "git.frostfs.info/TrueCloudLab/frostfs-sdk-go/container/id"
	"git.frostfs.info/TrueCloudLab/frostfs-sdk-go/pool"
	"git.frostfs.info/TrueCloudLab/frostfs-sdk-go/user"
	"github.com/nspcc-dev/neo-go/pkg/core/interop/interopnames"
	"github.com/nspcc-dev/neo-go/pkg/core/mempoolevent"
	"github.com/nspcc-dev/neo-go/pkg/core/native"
	"github.com/nspcc-dev/neo-go/pkg/core/transaction"
	"github.com/nspcc-dev/neo-go/pkg/crypto/keys"
	"github.com/nspcc-dev/neo-go/pkg/encoding/base58"
	"github.com/nspcc-dev/neo-go/pkg/encoding/bigint"
	"github.com/nspcc-dev/neo-go/pkg/neorpc/result"
	"github.com/nspcc-dev/neo-go/pkg/network/payload"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/actor"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/gas"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/nep17"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/notary"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/unwrap"
	"github.com/nspcc-dev/neo-go/pkg/smartcontract/callflag"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"github.com/nspcc-dev/neo-go/pkg/vm"
	"github.com/nspcc-dev/neo-go/pkg/vm/opcode"
	"github.com/nspcc-dev/neo-go/pkg/vm/stackitem"
	"github.com/nspcc-dev/neo-go/pkg/wallet"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const (
	cfgRPCEndpoint      = "rpc_endpoint"
	cfgRPCEndpointWS    = "rpc_endpoint_ws"
	cfgWallet           = "wallet"
	cfgPassword         = "password"
	cfgNnsContract      = "nns_contract"
	cfgStorageNode      = "storage_node"
	cfgStorageContainer = "storage_container"
	cfgListenAddress    = "listen_address"
	cfgTicketApiUrl     = "ticket_api_url"
)

var currentOperation = ""

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM) // если пользователь нажмет ctrl+C, то завершим выполнение

	if len(os.Args) != 2 { // go run client/main.go client/config.yml - команда запуска, проверяем, что параметров 2
		die(fmt.Errorf("invalid args: %v", os.Args))
	}

	viper.GetViper().SetConfigType("yml") // конфиг написан в формате yaml

	f, err := os.Open(os.Args[1]) // открываем конфиг
	die(err)
	die(viper.GetViper().ReadConfig(f)) // считываем
	die(f.Close())                      // закрываем

	s, err := NewServer(ctx)
	die(err)

	die(s.Listen(ctx))
}

type Server struct {
	p           *pool.Pool      // пул = обертка над клиентом, который умеет работать со storage node
	acc         *wallet.Account // кошелек, который будет платить за транзакции (вместо клиентского кошелька)
	act         *actor.Actor
	gasAct      *nep17.Token
	nftHash     util.Uint160
	auctionHash util.Uint160
	nnsHash     util.Uint160
	cnrID       cid.ID // Id контейнера в frost fs для хранения данных
	log         *zap.Logger
	rpcCli      *rpcclient.Client
	sub         subscriber.Subscriber // подписчик на события bc
	apiUrl      string
}

func NewServer(ctx context.Context) (*Server, error) {
	rpcCli, err := rpcclient.New(ctx, viper.GetString(cfgRPCEndpoint), rpcclient.Options{}) // создание rpc клиента взаимодействия приложений
	// или пользователей с нодой bc, rpc_endpoint = "http://localhost:30333"
	if err != nil {
		return nil, err
	}

	w, err := wallet.NewWalletFromFile(viper.GetString(cfgWallet)) // загрузка кошелька
	if err != nil {
		return nil, err
	}

	acc := w.GetAccount(w.GetChangeAddress())                                  // из кошелька wallet1 получаем аккаунт (акк там один)
	if err = acc.Decrypt(viper.GetString(cfgPassword), w.Scrypt); err != nil { // подтверждаем акк паролем
		return nil, err
	}

	act, err := actor.NewSimple(rpcCli, acc)
	if err != nil {
		return nil, err
	}

	p, err := createPool(ctx, acc, viper.GetString(cfgStorageNode)) // для работы со storage node, которая находится на "localhost:8080"
	if err != nil {
		return nil, err
	}

	contractNnsHash, err := util.Uint160DecodeStringLE(viper.GetString(cfgNnsContract))
	if err != nil {
		return nil, err
	}

	contractNftHash, err := ParseNnsResolve("nft.auc", contractNnsHash, act)
	if err != nil {
		return nil, err
	}

	contractAuctionHash, err := ParseNnsResolve("auc.auc", contractNnsHash, act)
	if err != nil {
		return nil, err
	}

	ticketApiUrl := viper.GetString(cfgTicketApiUrl)

	var cnrID cid.ID
	if err = cnrID.DecodeString(viper.GetString(cfgStorageContainer)); err != nil {
		return nil, err
	}

	neoClient, err := morphclient.New(ctx, acc.PrivateKey(),
		morphclient.WithEndpoints(morphclient.Endpoint{Address: viper.GetString(cfgRPCEndpointWS), Priority: 1}),
	) // morphclient - обертка над клинтом, который умеет слушать нотификации из цепочки; rpc_endpoint_ws отличается от обычного rpc_endpoint тем, что rpc_endpoint_ws автоматически уведомляет нас
	// о событиях (если мы слушаем его), а к rpc_endpoint нужно непрерывно обращаться, чтобы получить что-то
	if err != nil {
		return nil, fmt.Errorf("new morph client: %w", err)
	}

	if err = neoClient.EnableNotarySupport(); err != nil {
		return nil, err
	}

	params := new(subscriber.Params) // создания подписчика на события bc
	params.Client = neoClient
	l, err := logger.NewLogger(nil)
	if err != nil {
		return nil, err
	}
	params.Log = l
	sub, err := subscriber.New(ctx, params)
	if err != nil {
		return nil, err
	}

	if err = sub.SubscribeForNotaryRequests(acc.ScriptHash()); err != nil { // подписываемся на события нотариальных запросов в bc
		return nil, err
	}

	log, err := zap.NewDevelopment() // создание логгера
	if err != nil {
		return nil, err
	}

	return &Server{
		p:           p,
		acc:         acc,
		act:         act,
		rpcCli:      rpcCli,
		nftHash:     contractNftHash,
		auctionHash: contractAuctionHash,
		nnsHash:     contractNnsHash,
		gasAct:      nep17.New(act, gas.Hash),
		cnrID:       cnrID,
		log:         log,
		sub:         sub,
		apiUrl:      ticketApiUrl,
	}, nil
}

func ParseNnsResolve(domainName string, contractNnsHash util.Uint160, act *actor.Actor) (util.Uint160, error) {
	res, _ := act.Call(contractNnsHash, "resolve", domainName, 16)
	item := res.Stack[0]
	var output []string
	switch v := item.Value().(type) {
	case []stackitem.Item:
		for _, innerItem := range v {
			if innerBytes, ok := innerItem.Value().([]byte); ok { // Проверяем, является ли элемент ByteString
				output = append(output, string(innerBytes)) // Преобразуем ByteString в строку
			} else {
				return util.Uint160{}, fmt.Errorf("unexpected type in nns resolve "+domainName+" output array item: %T", innerItem.Value())
			}
		}
	default:
		return util.Uint160{}, fmt.Errorf("unexpected type in nns resolve "+domainName+" output array: %T", v)
	}
	decoded, err := base58.CheckDecode(output[0])
	if err != nil {
		return util.Uint160{}, err
	}
	contractHashStr := hex.EncodeToString(decoded)[2:]
	contractHash, err := util.Uint160DecodeStringBE(contractHashStr)
	if err != nil {
		return util.Uint160{}, err
	}

	return contractHash, nil
}

func (s *Server) Listen(ctx context.Context) error {
	if err := s.notaryDeposit(s.acc.ScriptHash()); err != nil { // накидываем себе (серверу) НД
		return fmt.Errorf("notary backend deposit: %w", err)
	}

	go s.runNotaryValidator(ctx) // // запускается слушатель нотариальных запросов в отдельной горутине (фоновый процесс)

	// обработчики запросов, которые слушают на 5555

	http.DefaultServeMux.HandleFunc("/balance", func(w http.ResponseWriter, r *http.Request) {
		s.log.Info("balance request")

		res, err := s.gasAct.BalanceOf(s.acc.ScriptHash())
		if err != nil {
			s.log.Error("balance error", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		if _, err = w.Write([]byte(strconv.FormatInt(res.Int64(), 10))); err != nil {
			s.log.Error("write response error", zap.Error(err))
		}
	})

	http.DefaultServeMux.HandleFunc("/properties/{tokenID}", func(w http.ResponseWriter, r *http.Request) { // обработчик запроса "посмотреть свойства указанного nft токена"
		s.log.Info("properties request")

		tokenIDStr := r.PathValue("tokenID")
		tokenID, err := hex.DecodeString(tokenIDStr)
		if err != nil {
			s.log.Error("invalid token ID", zap.String("tokenID", tokenIDStr), zap.Error(err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		m, err := unwrap.Map(s.act.Call(s.nftHash, "properties", tokenID))
		if err != nil {
			s.log.Error("call properties", zap.String("tokenID", tokenIDStr), zap.Error(err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		props, err := parseMap(m)
		if err != nil {
			s.log.Error("parse properties", zap.String("tokenID", tokenIDStr), zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		data, err := json.Marshal(props)
		if err != nil {
			s.log.Error("parse properties", zap.String("tokenID", tokenIDStr), zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		if _, err = w.Write(data); err != nil {
			s.log.Error("write response error", zap.Error(err))
		}
	})

	http.DefaultServeMux.HandleFunc("/notary-deposit/{userAddress}", func(w http.ResponseWriter, r *http.Request) { // накинуть НД по нужному адресу (клиент
		// этот запрос дергает, чтобы себе получить НД)
		s.log.Info("notary-deposit request", zap.String("url", r.URL.String()))

		var userID user.ID
		err := userID.DecodeString(r.PathValue("userAddress"))
		if err != nil {
			s.log.Error("invalid user address", zap.String("address", r.PathValue("userAddress")), zap.Error(err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		sh, err := userID.ScriptHash()
		if err != nil {
			s.log.Error("invalid user script hash", zap.Error(err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err = s.notaryDeposit(sh); err != nil {
			s.log.Error("failed to notary deposit", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	return http.ListenAndServe(viper.GetString(cfgListenAddress), nil)
}

func (s *Server) runNotaryValidator(ctx context.Context) { // слушатель НЗ из bc

	s.log.Info("start listening")

	for {
		select {
		case <-ctx.Done():
			die(ctx.Err())
		case notaryEvent, ok := <-s.sub.NotificationChannels().NotaryRequestsCh: // ждем события из канала NotaryRequestsCh,
			// который предоставляет уведомления о нотариальных запросах
			if !ok {
				return
			}
			s.log.Info("notary request", zap.String("hash", notaryEvent.NotaryRequest.Hash().String()),
				zap.String("main", notaryEvent.NotaryRequest.MainTransaction.Hash().String()),
				zap.String("fb", notaryEvent.NotaryRequest.FallbackTransaction.Hash().String()))

			switch notaryEvent.Type {
			case mempoolevent.TransactionAdded:
				scriptHash, tokenName, nftIdBytes, bet, err := s.parseNotaryEvent(notaryEvent)
				if err != nil {
					s.log.Error("parse notary event", zap.Error(err))
					continue
				}

				nAct := s.notaryActor(notaryEvent.NotaryRequest.MainTransaction.Scripts[1])

				var isMain bool
				switch currentOperation {
				case "mint":
					isMain, err = s.checkNotaryRequestGetNft(nAct, tokenName)
					if err != nil {
						s.log.Error("check notary request mint", zap.Error(err))
						continue
					}
				case "start":
					isMain, err = s.checkNotaryRequestStartAuction(nAct, scriptHash, nftIdBytes, bet)
					if err != nil {
						s.log.Error("check notary request start", zap.Error(err))
						continue
					}
				case "makeBet":
					isMain, err = s.checkNotaryRequestMakeBet(nAct, scriptHash, bet)
					if err != nil {
						s.log.Error("check notary request makeBet", zap.Error(err))
						continue
					}
				case "finish":
					isMain, err = s.checkNotaryRequestFinishAuction(nAct, scriptHash)
					if err != nil {
						s.log.Error("check notary request finish", zap.Error(err))
						continue
					}
				}

				if isMain {
					switch currentOperation {
					case "mint":
						err = s.proceedMainTxGetNft(ctx, nAct, notaryEvent, tokenName)
					case "start":
						err = s.proceedMainTxStartAuction(nAct, notaryEvent)
					case "makeBet":
						err = s.proceedMainTxMakeBet(nAct, notaryEvent)
					case "finish":
						err = s.proceedMainTxFinishAuction(nAct, notaryEvent)
					}

				} else {
					err = s.proceedFbTx(nAct, notaryEvent)
				}

				if err != nil {
					s.log.Error("proceed notary tx", zap.Bool("main", isMain), zap.String("token", tokenName), zap.Error(err))
				} else {
					s.log.Info("proceed notary tx", zap.Bool("main", isMain), zap.String("token", tokenName))
				}
			}
		}
	}
}

func (s *Server) parseNotaryEvent(notaryEvent *result.NotaryRequestEvent) (util.Uint160, string, []byte, int, error) {
	if len(notaryEvent.NotaryRequest.MainTransaction.Signers) != 3 { // подписанты:  1 - backend , который за все платит, 2 - client, который принимает на свой счет nft,
		// 3 - нотариальный контракт сам по себе, чья подпись необходима, чтобы  нотариальный запрос состоялся
		return util.Uint160{}, "", nil, 0, errors.New("error not enough signers")
	}

	if notaryEvent.NotaryRequest.Witness.ScriptHash().Equals(s.acc.ScriptHash()) {
		return util.Uint160{}, "", nil, 0, fmt.Errorf("ignore owned notary request: %s", notaryEvent.NotaryRequest.Hash().String())
	}

	scriptHash, tokenName, nftIdBytes, initBet, err := validateNotaryRequest(notaryEvent.NotaryRequest, s)
	if err != nil {
		return util.Uint160{}, "", nil, 0, err
	}

	return scriptHash, tokenName, nftIdBytes, initBet, err
}

func validateNotaryRequest(req *payload.P2PNotaryRequest, s *Server) (util.Uint160, string, []byte, int, error) {
	var (
		opCode opcode.Opcode // мб = PUSH, CALL, RET и тп
		param  []byte        // параметры инструкции
	)

	ctx := vm.NewContext(req.MainTransaction.Script) // контекст vm, будем пошагаво разбирать байт код
	ops := make([]Op, 0, 20)                         // 20 is maximum num of opcodes for calling contracts with 4 args(no arrays of arrays)

	var err error
	for {
		opCode, param, err = ctx.Next()
		if err != nil {
			return util.Uint160{}, "", nil, 0, fmt.Errorf("could not get next opcode in script: %w", err)
		}

		if opCode == opcode.RET {
			break
		}

		ops = append(ops, Op{code: opCode, param: param})
	}

	opsLen := len(ops)

	contractMethod := string(ops[opsLen-3].param) // название метода - 3я с конца инструкция
	currentOperation = contractMethod
	var (
		sh         util.Uint160
		tokenName  string
		nftIdBytes []byte
		bet        int
	)

	switch contractMethod {
	case "mint":
		sh, tokenName, err = validateNotaryRequestGetNft(req, s)
	case "start":
		sh, nftIdBytes, bet, err = validateNotaryRequestStartAuction(req, s)
	case "makeBet":
		sh, bet, err = validateNotaryRequestMakeBet(req, s)
	case "finish":
		err = validateNotaryRequestFinishAuction(req, s)
	default:
		fmt.Printf("Unknown contractMethod: %s\n", contractMethod)
	}

	return sh, tokenName, nftIdBytes, bet, err
}

func validateNotaryRequestPreProcessing(req *payload.P2PNotaryRequest) ([]Op, util.Uint160, error) {
	var (
		opCode opcode.Opcode
		param  []byte
	)

	ctx := vm.NewContext(req.MainTransaction.Script) // контекст vm, будем пошагаво разбирать байт код
	ops := make([]Op, 0, 20)

	var err error
	for {
		opCode, param, err = ctx.Next()
		if err != nil {
			return nil, util.Uint160{}, fmt.Errorf("could not get next opcode in script: %w", err)
		}

		if opCode == opcode.RET {
			break
		}

		ops = append(ops, Op{code: opCode, param: param})
	}

	opsLen := len(ops)

	contractSysCall := make([]byte, 4)
	binary.LittleEndian.PutUint32(contractSysCall, interopnames.ToID([]byte(interopnames.SystemContractCall)))
	// check if it is tx with contract call
	if !bytes.Equal(ops[opsLen-1].param, contractSysCall) {
		return nil, util.Uint160{}, errors.New("not contract syscall")
	}

	// retrieve contract's script hash
	contractHash, err := util.Uint160DecodeBytesBE(ops[opsLen-2].param) // вызываемый контракт - 2ая с конца инструкция
	if err != nil {
		return nil, util.Uint160{}, err
	}

	// check if there is a call flag(must be in range [0:15))
	callFlag := callflag.CallFlag(ops[opsLen-4].code - opcode.PUSH0)
	if callFlag > callflag.All {
		return nil, util.Uint160{}, fmt.Errorf("incorrect call flag: %s", callFlag)
	}

	args := ops[:opsLen-4]

	if len(args) != 0 {
		err = validateParameterOpcodes(args)
		if err != nil {
			return nil, util.Uint160{}, fmt.Errorf("could not validate arguments: %w", err)
		}

		// without args packing opcodes
		args = args[:len(args)-2]
	}

	return args, contractHash, err
}

func (s *Server) proceedFbTx(nAct *notary.Actor, notaryEvent *result.NotaryRequestEvent) error {
	err := nAct.Sign(notaryEvent.NotaryRequest.FallbackTransaction)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	_, err = nAct.Wait(nAct.Notarize(notaryEvent.NotaryRequest.FallbackTransaction, nil))
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}

	return nil
}

func parseMap(m *stackitem.Map) (map[string]string, error) {
	items := m.Value().([]stackitem.MapElement)
	res := make(map[string]string)

	for _, item := range items {
		k, err := item.Key.TryBytes()
		if err != nil {
			return nil, err
		}
		v, err := item.Value.TryBytes()
		if err != nil {
			return nil, err
		}

		kStr := string(k)

		switch kStr {
		case "id":
			res[kStr] = hex.EncodeToString(v)
		default:
			res[kStr] = string(v)
		}
	}

	return res, nil
}

func createPool(ctx context.Context, acc *wallet.Account, addr string) (*pool.Pool, error) { // создание пула соединений со storage node
	var prm pool.InitParameters
	prm.SetKey(&acc.PrivateKey().PrivateKey)   // SK сервера
	prm.AddNode(pool.NewNodeParam(1, addr, 1)) // storage node (localhost:8080)
	prm.SetNodeDialTimeout(5 * time.Second)    // max время ожидания для подключения к узлу

	p, err := pool.NewPool(prm)
	if err != nil {
		return nil, fmt.Errorf("new Pool: %w", err)
	}

	err = p.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	return p, nil
}

func (s *Server) notaryActor(userWitness transaction.Witness) *notary.Actor {
	pubBytes, ok := vm.ParseSignatureContract(userWitness.VerificationScript)
	if !ok {
		die(errors.New("invalid verification script"))
	}
	pub, err := keys.NewPublicKeyFromBytes(pubBytes, elliptic.P256())
	die(err)
	userAcc := notary.FakeSimpleAccount(pub)

	coSigners := []actor.SignerAccount{ // симметрично clientу
		{
			Signer: transaction.Signer{ // 1 подписант - backend (потому что платит первый подписант), данная программа, и мы она знает свой SK, его и ставит
				Account: s.acc.ScriptHash(),
				Scopes:  transaction.None,
			},
			Account: s.acc,
		},
		{
			Signer: transaction.Signer{
				Account: userAcc.ScriptHash(), // 2 подписант - не знаем SK clientа, т.к данная программа - backend, а не client, ставит PK clientа
				Scopes:  transaction.Global,
			},
			Account: userAcc,
		},
	}

	nAct, err := notary.NewActor(s.rpcCli, coSigners, s.acc)
	die(err)

	return nAct
}

func (s *Server) notaryDeposit(to util.Uint160) error { // на указанный адрес отправляем газ
	data := []any{to, int64(math.MaxUint32)}
	_, err := s.act.Wait(s.gasAct.Transfer(s.act.Sender(), notary.Hash, big.NewInt(1*native.GASFactor), data))
	return err
}

// Op is wrapper over Neo VM's opcode
// and its parameter.
type Op struct {
	code  opcode.Opcode
	param []byte
}

// Code returns Neo VM opcode.
func (o Op) Code() opcode.Opcode {
	return o.code
}

// Param returns parameter of wrapped
// Neo VM opcode.
func (o Op) Param() []byte {
	return o.param
}

// IntFromOpcode tries to retrieve int from Op.
func IntFromOpcode(op Op) (int64, error) {
	switch code := op.Code(); {
	case code == opcode.PUSHM1:
		return -1, nil
	case code >= opcode.PUSH0 && code <= opcode.PUSH16:
		return int64(code - opcode.PUSH0), nil
	case code <= opcode.PUSHINT256:
		return bigint.FromBytes(op.Param()).Int64(), nil
	default:
		return 0, fmt.Errorf("unexpected INT opcode %s", code)
	}
}

func validateParameterOpcodes(ops []Op) error {
	l := len(ops)

	if ops[l-1].code != opcode.PACK {
		return fmt.Errorf("unexpected packing opcode: %s", ops[l-1].code)
	}

	argsLen, err := IntFromOpcode(ops[l-2])
	if err != nil {
		return fmt.Errorf("could not parse argument len: %w", err)
	}

	err = validateNestedArgs(argsLen, ops[:l-2])
	return err
}

func validateNestedArgs(expArgLen int64, ops []Op) error {
	var (
		currentCode opcode.Opcode

		opsLenGot = len(ops)
	)

	for i := opsLenGot - 1; i >= 0; i-- {
		// only PUSH(also, PACK for arrays and CONVERT for booleans)
		// codes are allowed; number of params and their content must
		// be checked in a notary parser and a notary handler of a
		// particular contract
		switch currentCode = ops[i].code; {
		case currentCode <= opcode.PUSH16:
		case currentCode == opcode.CONVERT:
			if i == 0 || ops[i-1].code != opcode.PUSHT && ops[i-1].code != opcode.PUSHF {
				return errors.New("errUnexpectedCONVERT")
			}

			expArgLen++
		case currentCode == opcode.PACK:
			if i == 0 {
				return errors.New("errIncorrectArgPacking")
			}

			argsLen, err := IntFromOpcode(ops[i-1])
			if err != nil {
				return fmt.Errorf("could not parse argument len: %w", err)
			}

			expArgLen += argsLen + 1
			i--
		default:
			return fmt.Errorf("received main tx has unexpected(not PUSH) NeoVM opcode: %s", currentCode)
		}
	}

	if int64(opsLenGot) != expArgLen {
		return errors.New("errIncorrectArgPacking")
	}

	return nil
}

func die(err error) {
	if err == nil {
		return
	}

	debug.PrintStack()
	_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
