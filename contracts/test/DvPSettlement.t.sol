// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {Test} from "forge-std/Test.sol";
import {SEUR} from "../src/SEUR.sol";
import {TBILL} from "../src/TBILL.sol";
import {DvPSettlement} from "../src/DvPSettlement.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

contract DvPSettlementTest is Test {
    SEUR cash;
    TBILL security;
    DvPSettlement dvp;
    uint256 constant TRADER_KEY = 0xA11CE;
    uint256 constant MM_KEY = 0xB0B;
    address trader;
    address mm;

    function setUp() public {
        vm.warp(1_000);
        trader = vm.addr(TRADER_KEY);
        mm = vm.addr(MM_KEY);
        cash = new SEUR(address(this));
        security = new TBILL(address(this));
        dvp = new DvPSettlement(address(cash), address(security));
        cash.mint(trader, 100_000e6);
        cash.mint(mm, 90_000e6);
        security.mint(trader, 1_000e6);
        security.mint(mm, 1_200e6);
        for (uint256 i; i < 2; ++i) {
            vm.startPrank(i == 0 ? trader : mm);
            cash.approve(address(dvp), type(uint256).max);
            security.approve(address(dvp), type(uint256).max);
            vm.stopPrank();
        }
    }

    function trade(bool sell) internal view returns (DvPSettlement.Trade memory) {
        return DvPSettlement.Trade({
            tradeId: keccak256(bytes(sell ? "sell-100" : "buy-100")),
            buyer: sell ? mm : trader,
            seller: sell ? trader : mm,
            cashToken: address(cash),
            securityToken: address(security),
            cashAmount: sell ? 9_800_921500 : 9_804_921500,
            securityAmount: 100e6,
            deadline: 2_000
        });
    }

    function sign(uint256 key, bytes32 digest) internal pure returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(key, digest);
        return abi.encodePacked(r, s, v);
    }

    function signatures(DvPSettlement.Trade memory t)
        internal
        view
        returns (bytes memory buyer, bytes memory seller)
    {
        bytes32 digest = dvp.hashTrade(t);
        buyer = sign(t.buyer == trader ? TRADER_KEY : MM_KEY, digest);
        seller = sign(t.seller == trader ? TRADER_KEY : MM_KEY, digest);
    }

    function executeAndCheck(DvPSettlement.Trade memory t) internal {
        uint256 buyerCash = cash.balanceOf(t.buyer);
        uint256 sellerCash = cash.balanceOf(t.seller);
        uint256 buyerSecurity = security.balanceOf(t.buyer);
        uint256 sellerSecurity = security.balanceOf(t.seller);
        (bytes memory a, bytes memory b) = signatures(t);
        vm.expectEmit(true, true, true, true, address(dvp));
        emit DvPSettlement.TradeSettled(
            t.tradeId, t.buyer, t.seller, t.cashAmount, t.securityAmount
        );
        vm.prank(address(0x1234)); // The submitter is not a counterparty.
        dvp.settle(t, a, b);
        assertTrue(dvp.settled(t.buyer, t.seller, t.tradeId));
        assertEq(cash.balanceOf(t.buyer), buyerCash - t.cashAmount);
        assertEq(cash.balanceOf(t.seller), sellerCash + t.cashAmount);
        assertEq(security.balanceOf(t.buyer), buyerSecurity + t.securityAmount);
        assertEq(security.balanceOf(t.seller), sellerSecurity - t.securityAmount);
        assertEq(cash.balanceOf(address(dvp)), 0);
        assertEq(security.balanceOf(address(dvp)), 0);
        assertEq(cash.totalSupply(), 190_000e6);
        assertEq(security.totalSupply(), 2_200e6);
    }

    function testBuy() public {
        executeAndCheck(trade(false));
    }

    function testSell() public {
        executeAndCheck(trade(true));
    }

    function testFuzzExactTransfers(uint64 cashAmount, uint64 quantity, bool sell) public {
        DvPSettlement.Trade memory t = trade(sell);
        t.cashAmount = bound(cashAmount, 1, 90_000e6);
        t.securityAmount = bound(quantity, 1, 1_000e6);
        executeAndCheck(t);
    }

    function testReplayEvenWithChangedTerms() public {
        DvPSettlement.Trade memory t = trade(false);
        executeAndCheck(t);
        (bytes memory originalA, bytes memory originalB) = signatures(t);
        vm.expectRevert(DvPSettlement.TradeAlreadySettled.selector);
        dvp.settle(t, originalA, originalB);
        t.cashAmount += 1;
        (bytes memory a, bytes memory b) = signatures(t);
        vm.expectRevert(DvPSettlement.TradeAlreadySettled.selector);
        dvp.settle(t, a, b);
    }

    function testDeadlineBoundary() public {
        DvPSettlement.Trade memory t = trade(false);
        (bytes memory a, bytes memory b) = signatures(t);
        vm.warp(t.deadline);
        vm.expectRevert(DvPSettlement.TradeExpired.selector);
        dvp.settle(t, a, b);
        assertFalse(dvp.settled(t.buyer, t.seller, t.tradeId));
    }

    function testJustBeforeDeadline() public {
        vm.warp(1_999);
        executeAndCheck(trade(false));
    }

    function testFuzzAlteredSignedFields(uint8 field) public {
        DvPSettlement.Trade memory t = trade(false);
        (bytes memory a, bytes memory b) = signatures(t);
        uint256 choice = uint256(field) % 8;
        if (choice == 0) t.tradeId = keccak256("different");
        else if (choice == 1) t.buyer = address(0x123);
        else if (choice == 2) t.seller = address(0x456);
        else if (choice == 3) t.cashToken = address(security);
        else if (choice == 4) t.securityToken = address(cash);
        else if (choice == 5) t.cashAmount += 1;
        else if (choice == 6) t.securityAmount += 1;
        else t.deadline += 1;
        vm.expectRevert();
        dvp.settle(t, a, b);
        assertFalse(dvp.settled(t.buyer, t.seller, t.tradeId));
        assertEq(cash.balanceOf(trader), 100_000e6);
    }

    function testBothSignaturesRequired() public {
        DvPSettlement.Trade memory t = trade(false);
        (bytes memory a, bytes memory b) = signatures(t);
        vm.expectRevert(DvPSettlement.InvalidSignature.selector);
        dvp.settle(t, b, b);
        vm.expectRevert(DvPSettlement.InvalidSignature.selector);
        dvp.settle(t, a, a);
        vm.expectRevert();
        dvp.settle(t, a, hex"1234");
    }

    function testDifferentChainAndContract() public {
        DvPSettlement.Trade memory t = trade(false);
        (bytes memory a, bytes memory b) = signatures(t);
        uint256 chain = block.chainid;
        vm.chainId(chain + 1);
        vm.expectRevert(DvPSettlement.InvalidSignature.selector);
        dvp.settle(t, a, b);
        vm.chainId(chain);
        DvPSettlement other = new DvPSettlement(address(cash), address(security));
        vm.expectRevert(DvPSettlement.InvalidSignature.selector);
        other.settle(t, a, b);
    }

    function testSecondTransferRevertRollsBackAndAllowsSafeRetry() public {
        DvPSettlement.Trade memory t = trade(false);
        (bytes memory a, bytes memory b) = signatures(t);
        vm.prank(mm);
        security.approve(address(dvp), 0);
        vm.expectRevert();
        dvp.settle(t, a, b);
        assertEq(cash.balanceOf(trader), 100_000e6);
        assertEq(cash.balanceOf(mm), 90_000e6);
        assertEq(security.balanceOf(mm), 1_200e6);
        assertFalse(dvp.settled(t.buyer, t.seller, t.tradeId));
        vm.prank(mm);
        security.approve(address(dvp), 100e6);
        dvp.settle(t, a, b); // Signatures remain usable after a revert.
        assertTrue(dvp.settled(t.buyer, t.seller, t.tradeId));
    }

    function testFirstTransferRevert() public {
        DvPSettlement.Trade memory t = trade(false);
        (bytes memory a, bytes memory b) = signatures(t);
        vm.prank(trader);
        cash.approve(address(dvp), 0);
        vm.expectRevert();
        dvp.settle(t, a, b);
        assertFalse(dvp.settled(t.buyer, t.seller, t.tradeId));
        assertEq(security.balanceOf(mm), 1_200e6);
        assertEq(security.balanceOf(trader), 1_000e6);
    }

    function testInsufficientSellerBalance() public {
        DvPSettlement.Trade memory t = trade(true);
        (bytes memory a, bytes memory b) = signatures(t);
        vm.prank(trader);
        assertTrue(security.transfer(address(0x999), 1_000e6));
        vm.expectRevert();
        dvp.settle(t, a, b);
        assertEq(cash.balanceOf(mm), 90_000e6);
        assertEq(cash.balanceOf(trader), 100_000e6);
        assertFalse(dvp.settled(t.buyer, t.seller, t.tradeId));
    }

    function testInvalidTerms() public {
        for (uint256 i; i < 6; ++i) {
            DvPSettlement.Trade memory t = trade(false);
            if (i == 0) t.buyer = address(0);
            if (i == 1) t.seller = t.buyer;
            if (i == 2) t.cashAmount = 0;
            if (i == 3) t.securityAmount = 0;
            if (i == 4) t.tradeId = bytes32(0);
            if (i == 5) t.seller = address(dvp);
            vm.expectRevert(DvPSettlement.InvalidTrade.selector);
            dvp.settle(t, "", "");
        }
    }

    function testTokenConfiguration() public {
        vm.expectRevert(DvPSettlement.InvalidTokens.selector);
        new DvPSettlement(address(cash), address(cash));
        vm.expectRevert(DvPSettlement.InvalidTokens.selector);
        new DvPSettlement(address(0), address(security));
    }

    function testIdIsScopedToCounterparties() public {
        DvPSettlement.Trade memory buy = trade(false);
        executeAndCheck(buy);
        DvPSettlement.Trade memory sell = trade(true);
        sell.tradeId = buy.tradeId;
        executeAndCheck(sell);
    }

    function testCastTypedDataFixture() public {
        string memory json = vm.readFile("fixtures/buy-100.typed-data.json");
        string memory sigs = vm.readFile("fixtures/buy-100.signatures.json");
        address verifyingContract = vm.parseJsonAddress(json, ".domain.verifyingContract");
        vm.chainId(vm.parseJsonUint(json, ".domain.chainId"));
        vm.etch(verifyingContract, address(dvp).code);
        DvPSettlement.Trade memory t = DvPSettlement.Trade({
            tradeId: vm.parseJsonBytes32(json, ".message.tradeId"),
            buyer: vm.parseJsonAddress(json, ".message.buyer"),
            seller: vm.parseJsonAddress(json, ".message.seller"),
            cashToken: vm.parseJsonAddress(json, ".message.cashToken"),
            securityToken: vm.parseJsonAddress(json, ".message.securityToken"),
            cashAmount: vm.parseJsonUint(json, ".message.cashAmount"),
            securityAmount: vm.parseJsonUint(json, ".message.securityAmount"),
            deadline: vm.parseJsonUint(json, ".message.deadline")
        });
        bytes32 digest = DvPSettlement(verifyingContract).hashTrade(t);
        assertEq(ECDSA.recover(digest, vm.parseJsonBytes(sigs, ".buyerSignature")), t.buyer);
        assertEq(ECDSA.recover(digest, vm.parseJsonBytes(sigs, ".sellerSignature")), t.seller);
        assertEq(t.cashAmount, 9_804_921500);
        assertEq(t.securityAmount, 100e6);
    }

    function testIssuerAndDecimals() public {
        assertEq(cash.decimals(), 6);
        assertEq(security.decimals(), 6);
        vm.startPrank(trader);
        vm.expectRevert();
        cash.mint(trader, 1);
        vm.expectRevert();
        security.mint(trader, 1);
        vm.stopPrank();
        cash.mint(trader, 1);
        security.mint(trader, 1);
        assertEq(cash.totalSupply(), 190_000e6 + 1);
        assertEq(security.totalSupply(), 2_200e6 + 1);
    }
}
