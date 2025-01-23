neo-go contract compile --in ./nns_f --out nns_f/contract.nef -c nns_f/config.yml -m nns_f/contract.manifest.json
neo-go contract deploy --in nns_f/contract.nef --manifest nns_f/contract.manifest.json --await -r http://localhost:30333 -w ../../frostfs-aio/wallets/wallet1.json

neo-go contract invokefunction -r http://localhost:30333 -w ../../../frostfs-aio/morph/node-wallet.json -a NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP 8477fcff838587103b4d008a198a4a0c3a62a5b2 register   "nft.auc"  hash160:56c989e76f9a2ca05bb5caa6c96f524d905accd8   "almaxana_04@mail.ru"   100   100   31536000   31536000 -- 56c989e76f9a2ca05bb5caa6c96f524d905accd8:Global
neo-go contract invokefunction -r http://localhost:30333 -w ../../../frostfs-aio/morph/node-wallet.json -a NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP 8477fcff838587103b4d008a198a4a0c3a62a5b2 deleteDomain "nft.auc" -- 56c989e76f9a2ca05bb5caa6c96f524d905accd8:Global
neo-go contract testinvokefunction -r http://localhost:30333 8477fcff838587103b4d008a198a4a0c3a62a5b2 getRecords nft.auc 16
neo-go contract invokefunction -r http://localhost:30333 -w ../../../frostfs-aio/morph/node-wallet.json -a NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP 8477fcff838587103b4d008a198a4a0c3a62a5b2 addRecord   "nft.auc"  16 NLi8zS2QQTh4JBPRLGiegJeVepJLRJ3v4U -- 56c989e76f9a2ca05bb5caa6c96f524d905accd8:Global
neo-go contract invokefunction -r http://localhost:30333 -w ../../../frostfs-aio/morph/node-wallet.json -a NfgHwwTi3wHAS8aFAN243C5vGbkYDpqLHP 8477fcff838587103b4d008a198a4a0c3a62a5b2 deleteRecords   "nft.auc"  16 -- 56c989e76f9a2ca05bb5caa6c96f524d905accd8:Global
neo-go contract testinvokefunction -r http://localhost:30333 8477fcff838587103b4d008a198a4a0c3a62a5b2 resolve nft.auc 16
