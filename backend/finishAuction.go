package main

import (
	"fmt"

	"github.com/nspcc-dev/neo-go/pkg/neorpc/result"
	"github.com/nspcc-dev/neo-go/pkg/network/payload"
	"github.com/nspcc-dev/neo-go/pkg/rpcclient/notary"
	"github.com/nspcc-dev/neo-go/pkg/util"
	"go.uber.org/zap"
)

func (s *Server) proceedMainTxFinishAuction(nAct *notary.Actor, notaryEvent *result.NotaryRequestEvent) error {
	err := nAct.Sign(notaryEvent.NotaryRequest.MainTransaction)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	mainHash, fallbackHash, vub, err := nAct.Notarize(notaryEvent.NotaryRequest.MainTransaction, nil)
	s.log.Info("notarize sending",
		zap.String("hash", notaryEvent.NotaryRequest.Hash().String()),
		zap.String("main", mainHash.String()), zap.String("fb", fallbackHash.String()),
		zap.Uint32("vub", vub))

	_, err = nAct.Wait(mainHash, fallbackHash, vub, err)
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}

	return nil
}

func validateNotaryRequestFinishAuction(req *payload.P2PNotaryRequest, s *Server) error {
	args, contractHash, err := validateNotaryRequestPreProcessing(req)
	if err != nil {
		return err
	}

	contractHashExpected := s.auctionHash

	if !contractHash.Equals(contractHashExpected) {
		return fmt.Errorf("unexpected contract hash: %s", contractHash)
	}

	if len(args) != 1 {
		return fmt.Errorf("invalid param length: %d", len(args))
	}

	_, err = util.Uint160DecodeBytesBE(args[0].Param())
	if err != nil {
		return fmt.Errorf("could not decode script hash: %w", err)
	}

	return nil
}

func (s *Server) checkNotaryRequestFinishAuction(nAct *notary.Actor, finisher util.Uint160) (bool, error) {
	return true, nil
}
