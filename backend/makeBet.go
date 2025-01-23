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

func (s *Server) proceedMainTxMakeBet(nAct *notary.Actor, notaryEvent *result.NotaryRequestEvent) error {

	err := nAct.Sign(notaryEvent.NotaryRequest.MainTransaction) // sign transaction
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	// send notary query
	mainHash, fallbackHash, vub, err := nAct.Notarize(notaryEvent.NotaryRequest.MainTransaction, nil)
	if err != nil {
		return fmt.Errorf("notarize: %w", err)
	}

	s.log.Info("notarize sending",
		zap.String("hash", notaryEvent.NotaryRequest.MainTransaction.Hash().String()),
		zap.String("main", mainHash.String()),
		zap.String("fallback", fallbackHash.String()),
		zap.Uint32("vub", vub))

	_, err = nAct.Wait(mainHash, fallbackHash, vub, err)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}

	return nil
}

func validateNotaryRequestMakeBet(req *payload.P2PNotaryRequest, s *Server) (util.Uint160, int, error) {

	args, contractHash, err := validateNotaryRequestPreProcessing(req)
	if err != nil {
		return util.Uint160{}, 0, err
	}

	bet := int(binary.LittleEndian.Uint16(args[0].Param()))

	if len(args) != 2 {
		return util.Uint160{}, 0, fmt.Errorf("invalid param length: %d", len(args))
	}

	if !contractHash.Equals(s.auctionHash) {
		return util.Uint160{}, 0, fmt.Errorf("unexpected contract hash: %s", contractHash)
	}

	scriptHash, err := util.Uint160DecodeBytesBE(args[1].Param())
	if err != nil {
		return util.Uint160{}, 0, fmt.Errorf("could not decode script hash: %w", err)
	}

	return scriptHash, bet, err
}

func (s *Server) checkNotaryRequestMakeBet(nAct *notary.Actor, better util.Uint160, bet int) (bool, error) {
	return true, nil
}
