// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {Script} from "forge-std/Script.sol";
import {SEUR} from "../src/SEUR.sol";
import {TBILL} from "../src/TBILL.sol";
import {DvPSettlement} from "../src/DvPSettlement.sol";

contract DeployLocal is Script {
    // Mnemonic public par défaut d'Anvil, exclusivement pour une chaîne locale jetable.
    string constant MNEMONIC = "test test test test test test test test test test test junk";

    function run() external {
        require(block.chainid == 31337, "Anvil only");
        uint256 issuerKey = vm.deriveKey(MNEMONIC, 0);
        address issuer = vm.addr(issuerKey);
        vm.startBroadcast(issuerKey);
        SEUR cash = new SEUR(issuer);
        TBILL security = new TBILL(issuer);
        DvPSettlement dvp = new DvPSettlement(address(cash), address(security));
        for (uint32 i = 1; i <= 4; ++i) {
            address participant = vm.addr(vm.deriveKey(MNEMONIC, i));
            cash.mint(participant, i == 3 ? 90_000e6 : i == 4 ? 110_000e6 : 100_000e6);
            security.mint(participant, i == 3 ? 1_200e6 : i == 4 ? 800e6 : 1_000e6);
        }
        vm.stopBroadcast();
        for (uint32 i = 1; i <= 4; ++i) {
            vm.startBroadcast(vm.deriveKey(MNEMONIC, i));
            cash.approve(address(dvp), type(uint256).max);
            security.approve(address(dvp), type(uint256).max);
            vm.stopBroadcast();
        }
        vm.serializeAddress("deployment", "cashToken", address(cash));
        vm.serializeAddress("deployment", "securityToken", address(security));
        vm.serializeAddress("deployment", "settlement", address(dvp));
        vm.serializeAddress("deployment", "issuer", issuer);
        vm.serializeAddress("deployment", "trader", vm.addr(vm.deriveKey(MNEMONIC, 1)));
        vm.serializeAddress("deployment", "mm1", vm.addr(vm.deriveKey(MNEMONIC, 2)));
        vm.serializeAddress("deployment", "mm2", vm.addr(vm.deriveKey(MNEMONIC, 3)));
        vm.serializeAddress("deployment", "mm3", vm.addr(vm.deriveKey(MNEMONIC, 4)));
        vm.serializeAddress("deployment", "submitter", vm.addr(vm.deriveKey(MNEMONIC, 5)));
        string memory json = vm.serializeUint("deployment", "chainId", block.chainid);
        vm.createDir("deployments", true);
        // Les tests utilisent un manifeste distinct de la session interactive.
        vm.writeJson(json, vm.envOr("DEPLOYMENT_FILE", string("deployments/31337.json")));
    }
}
