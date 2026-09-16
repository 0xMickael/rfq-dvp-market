// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

contract DvPSettlement is EIP712, ReentrancyGuard {
    using SafeERC20 for IERC20;

    struct Trade {
        bytes32 tradeId;
        address buyer;
        address seller;
        address cashToken;
        address securityToken;
        uint256 cashAmount;
        uint256 securityAmount;
        uint256 deadline;
    }
    bytes32 public constant TRADE_TYPEHASH = keccak256(
        "Trade(bytes32 tradeId,address buyer,address seller,address cashToken,address securityToken,uint256 cashAmount,uint256 securityAmount,uint256 deadline)"
    );
    IERC20 public immutable cashToken;
    IERC20 public immutable securityToken;
    mapping(address buyer => mapping(address seller => mapping(bytes32 tradeId => bool))) public
        settled;
    error InvalidTokens();
    error InvalidTrade();
    error TradeExpired();
    error TradeAlreadySettled();
    error InvalidSignature();
    event TradeSettled(
        bytes32 indexed tradeId,
        address indexed buyer,
        address indexed seller,
        uint256 cashAmount,
        uint256 securityAmount
    );

    constructor(address cash, address security) EIP712("rfq-dvp-market", "1") {
        if (cash == security || cash.code.length == 0 || security.code.length == 0) {
            revert InvalidTokens();
        }
        cashToken = IERC20(cash);
        securityToken = IERC20(security);
    }

    function hashTrade(Trade calldata trade) public view returns (bytes32) {
        return _hashTypedDataV4(keccak256(abi.encode(TRADE_TYPEHASH, trade)));
    }

    function settle(
        Trade calldata trade,
        bytes calldata buyerSignature,
        bytes calldata sellerSignature
    ) external nonReentrant {
        if (trade.cashToken != address(cashToken) || trade.securityToken != address(securityToken)) revert InvalidTokens();
        if (
            trade.tradeId == bytes32(0) || trade.buyer == address(0) || trade.seller == address(0)
                || trade.buyer == trade.seller || trade.buyer == address(this)
                || trade.seller == address(this) || trade.cashAmount == 0
                || trade.securityAmount == 0
        ) revert InvalidTrade();
        if (block.timestamp >= trade.deadline) revert TradeExpired();
        if (settled[trade.buyer][trade.seller][trade.tradeId]) revert TradeAlreadySettled();
        bytes32 digest = hashTrade(trade);
        if (
            ECDSA.recover(digest, buyerSignature) != trade.buyer
                || ECDSA.recover(digest, sellerSignature) != trade.seller
        ) revert InvalidSignature();
        // Un revert de transfert annule aussi la consommation de l'identifiant.
        settled[trade.buyer][trade.seller][trade.tradeId] = true;
        cashToken.safeTransferFrom(trade.buyer, trade.seller, trade.cashAmount);
        securityToken.safeTransferFrom(trade.seller, trade.buyer, trade.securityAmount);
        emit TradeSettled(
            trade.tradeId, trade.buyer, trade.seller, trade.cashAmount, trade.securityAmount
        );
    }
}
