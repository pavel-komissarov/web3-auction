package main

import (
	"encoding/binary"
	"fmt"

	"github.com/nspcc-dev/neo-go/pkg/neorpc/result"
	"github.com/nspcc-dev/neo-go/pkg/network/payload"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/notary"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"go.uber.org/zap"
)

func (s *Server) proceedMainTxStartAuction(nAct *notary.Actor, notaryEvent *result.NotaryRequestEvent) error {
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

	return nil
}

func validateNotaryRequestStartAuction(req *payload.P2PNotaryRequest, s *Server) (util.Uint160, []byte, int, error) {

	args, contractHash, err := validateNotaryRequestPreProcessing(req)
	if err != nil {
		return util.Uint160{}, nil, 0, err
	}

	contractHashExpected := s.auctionHash // вызываемый контракт

	if !contractHash.Equals(contractHashExpected) {
		return util.Uint160{}, nil, 0, fmt.Errorf("unexpected contract hash: %s", contractHash)
	}

	if len(args) != 3 { // start принимает ровно 3 аргумента
		return util.Uint160{}, nil, 0, fmt.Errorf("invalid param length: %d", len(args))
	}

	nftIdBytes := args[1].Param()

	initBet := int(binary.LittleEndian.Uint16(args[0].Param()))

	sh, err := util.Uint160DecodeBytesBE(args[2].Param())
	if err != nil {
		return util.Uint160{}, nil, 0, fmt.Errorf("could not decode script hash: %w", err)
	}

	return sh, nftIdBytes, initBet, err
}

func (s *Server) checkNotaryRequestStartAuction(nAct *notary.Actor, organizer util.Uint160, lotId []byte, initBet int) (bool, error) {
	return true, nil
}
