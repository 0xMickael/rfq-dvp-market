// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;
import {Script} from "forge-std/Script.sol";
import {DvPSettlement} from "../src/DvPSettlement.sol";

contract SettleLocal is Script {
    string constant MNEMONIC = "test test test test test test test test test test test junk";

    function run() external {
        require(block.chainid == 31337, "Anvil only");
        string memory json = vm.readFile("deployments/31337.json");
        DvPSettlement dvp = DvPSettlement(vm.parseJsonAddress(json, ".settlement"));
        for (uint256 i; i < 2; ++i) {
            bool sell = i == 1;
            uint256 traderKey = vm.deriveKey(MNEMONIC, 1);
            uint256 makerKey = vm.deriveKey(MNEMONIC, sell ? 4 : 3);
            DvPSettlement.Trade memory t = DvPSettlement.Trade({
                tradeId: keccak256(bytes(sell ? "local-sell-100" : "local-buy-100")),
                buyer: vm.addr(sell ? makerKey : traderKey),
                seller: vm.addr(sell ? traderKey : makerKey),
                cashToken: address(dvp.cashToken()),
                securityToken: address(dvp.securityToken()),
                cashAmount: sell ? 9_800_921500 : 9_804_921500,
                securityAmount: 100e6,
                deadline: block.timestamp + 300
            });
            bytes32 digest = dvp.hashTrade(t);
            bytes memory a = sign(sell ? makerKey : traderKey, digest);
            bytes memory b = sign(sell ? traderKey : makerKey, digest);
            vm.startBroadcast(vm.deriveKey(MNEMONIC, 5));
            dvp.settle(t, a, b);
            vm.stopBroadcast();
        }
    }

    function sign(uint256 key, bytes32 digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(key, digest);
        return abi.encodePacked(r, s, v);
    }
}
