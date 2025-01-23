# Аукцион по продаже билета (NFT-токена)

## Тема проекта 
*Создание симуляции аукциона по продаже билета*

## Описание
Пользователи могут получать себе NFT с помощью вызова `getNFT`.

Если пользователь, имеющий NFT, хочет его выставить на аукцион, он вызывает функцию `startAuction`. В один момент времени в системе может быть не больше одного аукциона. 

Все пользователи получают уведомление о том, что в системе начался аукцион, и могут принять участие в нем. При помощи вызова `makeBet` они могут сделать ставку. При этом все пользователи получат уведомление о сделанной ставке. Каждая ставка должна быть выше предыдущей. Таким образом, пользователи стараются перебить ставки друг друга. Тот, кто поставил наибольшую ставку, по окончании аукциона заберет лот.  В процессе аукциона сохраняется последняя сделанная ставка и хеш кошелька, с которого она была сделана. Пока идет аукцион, можно смотреть актуальную информацию о нем: id лота, последнюю ставку, потенциального победителя, который заберет лот, если никто не перебьет его ставку до окончания аукциона. 

По истечении определенного времени организатор аукциона заканчивает его, вызывая `finishAuction`. Выставленный организатором лот автоматически отправляется с кошелька организатора на кошелек победителя аукциона. Если в процессе аукциона ни одна ставка не была сделана, значит лот останется у организатора аукциона.
Отметим, что победитель действительно получает лот на свой счет, теперь он является владельцем выигранного токена, но его ставка - это не реальные токены (это просто число), по завершении аукциона его ставка не спишется с его кошелька. На кошельках пользователей могут быть только NFT токены TICKET. А ставку, представленную чем-то реальным, при желании победитель отдаст организатору аукциона уже вне приложения.

Что представляет из себя лот? Мы разыгрываем ticket - NFTшку. NFT хранит адрес объекта во frost fs, где лежит url на определенный json. Json структура состоит мз таких полей как: id, eventName(название мероприятия), row(ряд), seat(место). Все json созданы при помощи mockAPI и их можно посмотреть по ссылке https://678b8b8a1a6b89b27a2aaf17.mockapi.io/jsonticket/tickets/ticket. 

## Структура приложения

1. client  - часть приложения, с которой непосредственно работает пользователь. client парсит функцию, вызванную пользователем и создает соответствующий нотариальный запрос (НЗ). НЗ позволяет осуществлять спонсируемые транзакции: т.к у пользователя на кошельке нет газа, чтобы платить за транзакции, вместо него за них платит backend. Программ client может быть запущено несколько на одном узле.
2. backend - часть приложения, которая слушает из сети НЗ клиентов. backend, уловив из сети НЗ, валидирует его и подписывает, а потом отправляет в сеть. backend на узле один
3. auction - основной контракт. Кроме функций deploy и update содержит функции начала и завершения аукциона , просмотра текущей ставки и лота, получения текущего победителя и "сделать ставку". 
4. nft - это ключевой контракт системы, отвечающий за создание, хранение и управление правами пользования уникальных токенов (билеты). Для работы nft в neo реализованы определенные методы стандарта NEP11
5. nns - вспомогательный контракт, который используется для разрешения имен контрактов в их хэши (аналог DNS)

## Зачем здесь блокчейн

Вызов любой функции аукциона (start, makeBet, finish) - транзакция, которая осядет в блокчейне и накатится на state всех нод. Это дает такие преимущества, как

 - безопасность
   
    никто не сможет изменить данные текущего аукциона незаметно (изменить ставку, подменить хэш победителя и тп);
нет центрального узла, на котором работает приложение, нет единой БД с информацией об аукционе, значит намного сложнее провести атаку на приложение, для этого потребутся огромные вычислительные мощности
       
- доступность
  
    в любой момент можно подключиться к сети с любой ноды и присоединиться к аукциону, увидеть свои NFT во frost fs

 - прозрачность
   
    все действия, производимые с аукционом, видны всем; история транзакций сохраняется в блоках, ее можно просмотреть

 - надежность
   
    нет центрального узла, на котором работает приложение. Если нода с запущенным приложением упала, то данные аукциона сохранятся, и доступ к ним можно будет получить с другой ноды, запустив приложение на ней
________________________________________________________________________________________________



## Инструкция по запуску
### Подготовка
Клонируем `frostfs-aio`, переходим на ветку `nightly-v1.7`. Если уже поднимали сеть и хотим все начать с чистого листа, то, чтобы удалить все работающие контейнеры вместе с задеплоенными контрактами пишем:
```bash
make down clean
```
Поднимаем `frostfs-aio`
```bash
make up
```

Кладем денег на `wallet1.json`, который будет платить от имени backend'a за транзакции :
 ```bash
make refill
```

Создаем контейнер - storage node
````bash
1)  make cred

2) в любой директории frostfs-aio создаем файл user-cli-cfg.yaml с содержимым:
wallet: /config/user-wallet.json
password: ""
rpc-endpoint: localhost:8080

3) docker cp user-cli-cfg.yaml aio:/config/user-cli-cfg.yaml
4) docker exec aio frostfs-cli container create -c /config/user-cli-cfg.yaml --policy 'REP 1' --await
5) docker exec aio frostfs-cli -c /config/user-cli-cfg.yaml ape-manager add --chain-id nyan --rule 'Allow Object.* *' --target-type container --target-name <CID полученный предыдущей командой>
````
❗️в `backend/config.yaml` в `storage_container` указываем полученный CID

### nns
```
neo-go contract compile --in ./nns --out nns/contract.nef -c nns/config.yml -m nns/contract.manifest.json
neo-go contract deploy --in nns/contract.nef --manifest nns/contract.manifest.json --await -r http://localhost:30333 -w ../../frostfs-aio/wallets/wallet1.json
```
❗️В `backend/config.yml` и `client/config.yml` в поле `nns_contract` записываем полученный хэш.
Затем этот хэш преобразуем в Address LE с помощью команды 
```
neo-go util convert <хэш>
```
❗️и записываем полученный адрес в поле `nnsContractHashString` контрактов `nft` и `auction`
## nft
Деплоим данный контракт от имени аккаунта ноды, который имеет статус committee. Мы взяли не простой кошелек `wallets/wallet1.json`, потому что вызов функций nns внутри контракта nft требует подписи коммитета. Пароль от аккаунта NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP  - `one`
```
neo-go contract compile -i nft/contract.go -o nft/contract.nef -m nft/contract.manifest.json -c nft/contract.yml
neo-go contract deploy -i nft/contract.nef -m nft/contract.manifest.json -r http://localhost:30333 -w ../../frostfs-aio/morph/node-wallet.json -a NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP [ NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP ] -- NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP:Global
```

### auction
Аналогично деплоим данный контракт от имени аккаунта ноды
```
neo-go contract compile --in auction/contract.go --out auction/contract.nef -c auction/contract.yml -m auction/contract.manifest.json
neo-go contract deploy -i auction/contract.nef -m auction/contract.manifest.json -r http://localhost:30333 -w ../../frostfs-aio/morph/node-wallet.json -a NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP -- NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP:Global
```
Если надо его обновить, то снова компилируем контракт и вызываем у него update
```
neo-go contract invokefunction -r http://localhost:30333 -w ../../frostfs-aio/wallets/wallet1.json 45c904b50922ded714019a49796dafbdd981247f update filebytes:contract.nef filebytes:contract.manifest.json [ ]
```

### backend

Запускаем backend

```bash
go run ./backend backend/config.yml
```

### client

Если нужно создать нового пользователя, то создаем для него кошелек командой
```
neo-go wallet init -a -w walletN.json
```
И записываем его в конфиг с именем configN.yml


Запускаем client

```bash
go run ./client client/config.yml
```

Он будет работать постоянно, так же как и backend. В терминале клиента нужно вводить команды. Примеры
```bash
getNFT
startAuction 	dce48fffd5f2b57c8c76c407e26da2a99dce8b59076fc7805c8e6326389c20fc 	300
makeBet 500
finishAuction
exit
```

### extra commands
Посмотреть, свойства данного nft
```
curl http://localhost:5555/properties/6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b | jq
```
Увидеть json, на который ссылается данный nft
```
http://localhost:8081/get/<address>
```

Можно вызвать непосредственно функции контракта auction из консоли (даны пары команд: первая для вызова функции контракта, вторая - для конвертации полученного ответа в человекочиатемый вид)
 - ShowLotId
```
neo-go contract testinvokefunction -r http://localhost:30333 	45c904b50922ded714019a49796dafbdd981247f showLotId
echo "nbWAJ75S0nDn7lc4XIcx2O68bG3rceLI6hHdxb1YgnM=" | base64 --decode | xxd -p
```

 - ShowCurrentBet
```
neo-go contract testinvokefunction -r http://localhost:30333 	5af416d1ec7825786474f26a92e3a8a772e22810 showCurrentBet
neo-go util convert 9AE=
```
