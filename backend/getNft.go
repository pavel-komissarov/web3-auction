package main

import (
	"context"
	"fmt"
	"net/http"

	"git.frostfs.info/TrueCloudLab/frostfs-sdk-go/object"
	"git.frostfs.info/TrueCloudLab/frostfs-sdk-go/pool"
	"git.frostfs.info/TrueCloudLab/frostfs-sdk-go/user"
	"github.com/nspcc-dev/neo-go/pkg/neorpc/result"
	"github.com/nspcc-dev/neo-go/pkg/network/payload"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/notary"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"go.uber.org/zap"
)

func validateNotaryRequestGetNft(req *payload.P2PNotaryRequest, s *Server) (util.Uint160, string, error) {
	args, contractHash, err := validateNotaryRequestPreProcessing(req)
	if err != nil {
		return util.Uint160{}, "", err
	}

	contractHashExpected := s.nftHash

	if !contractHash.Equals(contractHashExpected) {
		return util.Uint160{}, "", fmt.Errorf("unexpected contract hash: %s", contractHash)
	}

	// аргументы лежат в обратном порядке (как мы их передаем, только наоборот)
	if len(args) != 2 { // mint принимает ровно 2 аргумента
		return util.Uint160{}, "", fmt.Errorf("invalid param length: %d", len(args))
	}

	sh, err := util.Uint160DecodeBytesBE(args[1].Param())

	return sh, string(args[0].Param()), err
}

func (s *Server) proceedMainTxGetNft(ctx context.Context, nAct *notary.Actor, notaryEvent *result.NotaryRequestEvent, tokenName string) error {
	err := nAct.Sign(notaryEvent.NotaryRequest.MainTransaction)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	mainHash, fallbackHash, vub, err := nAct.Notarize(notaryEvent.NotaryRequest.MainTransaction, nil)
	s.log.Info("notarize sending",
		zap.String("hash", notaryEvent.NotaryRequest.Hash().String()),
		zap.String("main", mainHash.String()), zap.String("fb", fallbackHash.String()),
		zap.Uint32("vub", vub))

	_, err = nAct.Wait(mainHash, fallbackHash, vub, err) // ждем, пока какая-нибудь tx будет принята
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}

	url := s.apiUrl + tokenName

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("get url '%s' : %w", url, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			s.log.Error("close response bode", zap.Error(err))
		}
	}()

	var ownerID user.ID
	user.IDFromKey(&ownerID, s.acc.PrivateKey().PrivateKey.PublicKey)

	obj := object.New()
	obj.SetContainerID(s.cnrID)
	obj.SetOwnerID(ownerID)

	var prm pool.PrmObjectPut
	prm.SetPayload(resp.Body)
	prm.SetHeader(*obj)

	objID, err := s.p.PutObject(ctx, prm)
	if err != nil {
		return fmt.Errorf("put object '%s': %w", url, err)
	}

	addr := s.cnrID.EncodeToString() + "/" + objID.ObjectID.EncodeToString()
	s.log.Info("put object", zap.String("url", url), zap.String("address", addr))

	_, err = s.act.Wait(s.act.SendCall(s.nftHash, "setAddress", tokenName, addr)) // добавляем адрес токену. После того, как произошел mint, заполнены у нового
	// nft будут поля, кроме address. Он будет добавляться отдельно здесь, после того, как токен создался.
	// Потому что пользователь должен знать, какую nft он хочет выписать
	if err != nil {
		return fmt.Errorf("wait setAddress: %w", err)
	}

	return nil
}

func (s *Server) checkNotaryRequestGetNft(nAct *notary.Actor, tokenName string) (bool, error) {
	return true, nil
}
