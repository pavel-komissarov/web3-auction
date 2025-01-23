package nns

import (
	"github.com/nspcc-dev/neo-go/pkg/interop"
	"github.com/nspcc-dev/neo-go/pkg/interop/contract"
	"github.com/nspcc-dev/neo-go/pkg/interop/native/management"
	"github.com/nspcc-dev/neo-go/pkg/interop/native/std"
	"github.com/nspcc-dev/neo-go/pkg/interop/storage"
)

const FrostfsIDNNSName = "frostfsid.frostfs"

const (
	FrostfsIDNNSTLDPermissionKey    = "nns-allow-register-tld"
	FrostfsIDTLDRegistrationAllowed = "allow"
)

func checkFrostfsID(ctx storage.Context, addr interop.Hash160) bool {
	if len(addr) == 0 {
		return false
	}

	frostfsIDAddress := getRecordsByType(ctx, []byte(tokenIDFromName(FrostfsIDNNSName)), FrostfsIDNNSName, TXT)
	if len(frostfsIDAddress) < 2 {
		return false
	}

	decodedBytes := std.Base58Decode([]byte(frostfsIDAddress[1]))

	if len(decodedBytes) < 21 || management.GetContract(decodedBytes[1:21]) == nil {
		return false
	}

	if res := contract.Call(decodedBytes[1:21], "getSubjectKV", contract.ReadOnly, addr, FrostfsIDNNSTLDPermissionKey).(string); res == FrostfsIDTLDRegistrationAllowed {
		return true
	}

	return false
}
