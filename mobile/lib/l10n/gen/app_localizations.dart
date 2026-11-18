import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:intl/intl.dart' as intl;

import 'app_localizations_en.dart';
import 'app_localizations_zh.dart';

// ignore_for_file: type=lint

/// Callers can lookup localized strings with an instance of AppLocalizations
/// returned by `AppLocalizations.of(context)`.
///
/// Applications need to include `AppLocalizations.delegate()` in their app's
/// `localizationDelegates` list, and the locales they support in the app's
/// `supportedLocales` list. For example:
///
/// ```dart
/// import 'gen/app_localizations.dart';
///
/// return MaterialApp(
///   localizationsDelegates: AppLocalizations.localizationsDelegates,
///   supportedLocales: AppLocalizations.supportedLocales,
///   home: MyApplicationHome(),
/// );
/// ```
///
/// ## Update pubspec.yaml
///
/// Please make sure to update your pubspec.yaml to include the following
/// packages:
///
/// ```yaml
/// dependencies:
///   # Internationalization support.
///   flutter_localizations:
///     sdk: flutter
///   intl: any # Use the pinned version from flutter_localizations
///
///   # Rest of dependencies
/// ```
///
/// ## iOS Applications
///
/// iOS applications define key application metadata, including supported
/// locales, in an Info.plist file that is built into the application bundle.
/// To configure the locales supported by your app, you’ll need to edit this
/// file.
///
/// First, open your project’s ios/Runner.xcworkspace Xcode workspace file.
/// Then, in the Project Navigator, open the Info.plist file under the Runner
/// project’s Runner folder.
///
/// Next, select the Information Property List item, select Add Item from the
/// Editor menu, then select Localizations from the pop-up menu.
///
/// Select and expand the newly-created Localizations item then, for each
/// locale your application supports, add a new item and select the locale
/// you wish to add from the pop-up menu in the Value field. This list should
/// be consistent with the languages listed in the AppLocalizations.supportedLocales
/// property.
abstract class AppLocalizations {
  AppLocalizations(String locale)
    : localeName = intl.Intl.canonicalizedLocale(locale.toString());

  final String localeName;

  static AppLocalizations of(BuildContext context) {
    return Localizations.of<AppLocalizations>(context, AppLocalizations)!;
  }

  static const LocalizationsDelegate<AppLocalizations> delegate =
      _AppLocalizationsDelegate();

  /// A list of this localizations delegate along with the default localizations
  /// delegates.
  ///
  /// Returns a list of localizations delegates containing this delegate along with
  /// GlobalMaterialLocalizations.delegate, GlobalCupertinoLocalizations.delegate,
  /// and GlobalWidgetsLocalizations.delegate.
  ///
  /// Additional delegates can be added by appending to this list in
  /// MaterialApp. This list does not have to be used at all if a custom list
  /// of delegates is preferred or required.
  static const List<LocalizationsDelegate<dynamic>> localizationsDelegates =
      <LocalizationsDelegate<dynamic>>[
        delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
      ];

  /// A list of this localizations delegate's supported locales.
  static const List<Locale> supportedLocales = <Locale>[
    Locale('en'),
    Locale('zh'),
  ];

  /// Product name shown in English UI. Chinese UI uses Hoxi.
  ///
  /// In en, this message translates to:
  /// **'Hoxi'**
  String get appName;

  /// No description provided for @navChat.
  ///
  /// In en, this message translates to:
  /// **'Chat'**
  String get navChat;

  /// No description provided for @commonCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get commonCancel;

  /// No description provided for @commonRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get commonRefresh;

  /// No description provided for @chatComposerSkillHint.
  ///
  /// In en, this message translates to:
  /// **'Message @{skill} or hold to talk…'**
  String chatComposerSkillHint(String skill);

  /// No description provided for @chatSend.
  ///
  /// In en, this message translates to:
  /// **'Send message'**
  String get chatSend;

  /// No description provided for @chatHideKeyboard.
  ///
  /// In en, this message translates to:
  /// **'Hide keyboard'**
  String get chatHideKeyboard;

  /// No description provided for @loginSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Link Hoxi Desktop or a GoPanel panel'**
  String get loginSubtitle;

  /// No description provided for @loginNodeAddress.
  ///
  /// In en, this message translates to:
  /// **'Device address'**
  String get loginNodeAddress;

  /// No description provided for @loginAddressHint.
  ///
  /// In en, this message translates to:
  /// **'http://192.168.1.x:port or https://demo.gopanel.run'**
  String get loginAddressHint;

  /// No description provided for @loginAddressRequired.
  ///
  /// In en, this message translates to:
  /// **'Enter the device address'**
  String get loginAddressRequired;

  /// No description provided for @loginAddressScheme.
  ///
  /// In en, this message translates to:
  /// **'Enter a full URL starting with http/https'**
  String get loginAddressScheme;

  /// No description provided for @loginPassword.
  ///
  /// In en, this message translates to:
  /// **'Password'**
  String get loginPassword;

  /// No description provided for @loginPasswordHint.
  ///
  /// In en, this message translates to:
  /// **'Enter the password (optional on Hoxi Desktop)'**
  String get loginPasswordHint;

  /// No description provided for @loginPasswordRequired.
  ///
  /// In en, this message translates to:
  /// **'Enter the password'**
  String get loginPasswordRequired;

  /// No description provided for @loginConnect.
  ///
  /// In en, this message translates to:
  /// **'Connect'**
  String get loginConnect;

  /// No description provided for @loginScanQr.
  ///
  /// In en, this message translates to:
  /// **'Scan QR code'**
  String get loginScanQr;

  /// No description provided for @loginConnectFailed.
  ///
  /// In en, this message translates to:
  /// **'Connection failed'**
  String get loginConnectFailed;

  /// No description provided for @engineeringAddNode.
  ///
  /// In en, this message translates to:
  /// **'Link a device'**
  String get engineeringAddNode;

  /// No description provided for @loginEntranceLabel.
  ///
  /// In en, this message translates to:
  /// **'Security entrance (optional)'**
  String get loginEntranceLabel;

  /// No description provided for @loginEntranceHint.
  ///
  /// In en, this message translates to:
  /// **'For example: your-entrance'**
  String get loginEntranceHint;

  /// No description provided for @loginEntranceInvalid.
  ///
  /// In en, this message translates to:
  /// **'Use only letters, numbers, underscores and hyphens'**
  String get loginEntranceInvalid;

  /// No description provided for @loginAdminEmail.
  ///
  /// In en, this message translates to:
  /// **'Administrator email'**
  String get loginAdminEmail;

  /// No description provided for @loginAdminEmailHint.
  ///
  /// In en, this message translates to:
  /// **'Enter the administrator email'**
  String get loginAdminEmailHint;

  /// No description provided for @chatPendingApprovals.
  ///
  /// In en, this message translates to:
  /// **'{count} pending approval(s)'**
  String chatPendingApprovals(int count);

  /// No description provided for @chatModelPickerTitle.
  ///
  /// In en, this message translates to:
  /// **'Select model'**
  String get chatModelPickerTitle;

  /// No description provided for @chatTooltipHoldToTalk.
  ///
  /// In en, this message translates to:
  /// **'Hold to talk'**
  String get chatTooltipHoldToTalk;

  /// No description provided for @chatTooltipCamera.
  ///
  /// In en, this message translates to:
  /// **'Camera'**
  String get chatTooltipCamera;

  /// No description provided for @chatCameraUnavailable.
  ///
  /// In en, this message translates to:
  /// **'Camera unavailable. Check permission; cancelling does not send a photo.'**
  String get chatCameraUnavailable;

  /// No description provided for @chatListeningShort.
  ///
  /// In en, this message translates to:
  /// **'Listening…'**
  String get chatListeningShort;

  /// No description provided for @settingsOverviewTitle.
  ///
  /// In en, this message translates to:
  /// **'Me'**
  String get settingsOverviewTitle;

  /// No description provided for @addNodeHoxiTitle.
  ///
  /// In en, this message translates to:
  /// **'Hoxi Desktop'**
  String get addNodeHoxiTitle;

  /// No description provided for @addNodeHoxiSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Connect desktop Hoxi workspace for AI coding and development'**
  String get addNodeHoxiSubtitle;

  /// No description provided for @addNodeGopanelTitle.
  ///
  /// In en, this message translates to:
  /// **'GoPanel panel'**
  String get addNodeGopanelTitle;

  /// No description provided for @addNodeGopanelSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Connect a server panel for hosts, containers, sites and certificates'**
  String get addNodeGopanelSubtitle;

  /// No description provided for @addNodeComingSoon.
  ///
  /// In en, this message translates to:
  /// **'Coming soon'**
  String get addNodeComingSoon;

  /// No description provided for @loginHoxiTitle.
  ///
  /// In en, this message translates to:
  /// **'Link Hoxi Desktop'**
  String get loginHoxiTitle;

  /// No description provided for @loginHoxiSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Scan desktop QR code, or enter the LAN/relay address'**
  String get loginHoxiSubtitle;

  /// No description provided for @loginGopanelTitle.
  ///
  /// In en, this message translates to:
  /// **'Link a GoPanel panel'**
  String get loginGopanelTitle;

  /// No description provided for @loginGopanelSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Sign in with a panel administrator account'**
  String get loginGopanelSubtitle;

  /// No description provided for @loginPanelAddress.
  ///
  /// In en, this message translates to:
  /// **'Panel address'**
  String get loginPanelAddress;

  /// No description provided for @loginPanelAddressHint.
  ///
  /// In en, this message translates to:
  /// **'https://demo.gopanel.run'**
  String get loginPanelAddressHint;

  /// No description provided for @loginHoxiAddress.
  ///
  /// In en, this message translates to:
  /// **'Desktop address'**
  String get loginHoxiAddress;

  /// No description provided for @loginHoxiAddressHint.
  ///
  /// In en, this message translates to:
  /// **'http://192.168.x.x:5417 or relay address'**
  String get loginHoxiAddressHint;

  /// No description provided for @loginHoxiToken.
  ///
  /// In en, this message translates to:
  /// **'Pairing token (optional)'**
  String get loginHoxiToken;

  /// No description provided for @loginHoxiTokenHint.
  ///
  /// In en, this message translates to:
  /// **'Enter pairing token if configured on desktop'**
  String get loginHoxiTokenHint;

  /// No description provided for @loginHoxiQrHint.
  ///
  /// In en, this message translates to:
  /// **'Scan QR code in Hoxi Desktop Settings -> Remote Access'**
  String get loginHoxiQrHint;

  /// No description provided for @loginScanHoxiQr.
  ///
  /// In en, this message translates to:
  /// **'Scan desktop QR code'**
  String get loginScanHoxiQr;

  /// No description provided for @loginScanPanelQr.
  ///
  /// In en, this message translates to:
  /// **'Scan panel QR code'**
  String get loginScanPanelQr;

  /// No description provided for @loginUsername.
  ///
  /// In en, this message translates to:
  /// **'Account (optional)'**
  String get loginUsername;

  /// No description provided for @loginPasswordHintPanel.
  ///
  /// In en, this message translates to:
  /// **'Enter the administrator password'**
  String get loginPasswordHintPanel;

  /// No description provided for @settingsDeviceRenameHint.
  ///
  /// In en, this message translates to:
  /// **'Give this device a name you will recognize'**
  String get settingsDeviceRenameHint;

  /// No description provided for @commonSave.
  ///
  /// In en, this message translates to:
  /// **'Save'**
  String get commonSave;

  /// No description provided for @qrErrorMissingUrl.
  ///
  /// In en, this message translates to:
  /// **'Invalid QR code: missing device address'**
  String get qrErrorMissingUrl;

  /// No description provided for @qrErrorMissingToken.
  ///
  /// In en, this message translates to:
  /// **'Invalid QR code: missing panel address or credentials'**
  String get qrErrorMissingToken;

  /// No description provided for @qrErrorWrongKindPanel.
  ///
  /// In en, this message translates to:
  /// **'This is a panel QR code. Go back and choose GoPanel panel'**
  String get qrErrorWrongKindPanel;

  /// No description provided for @qrErrorWrongKindHoxi.
  ///
  /// In en, this message translates to:
  /// **'This is a desktop QR code. Go back and choose Hoxi Desktop'**
  String get qrErrorWrongKindHoxi;

  /// No description provided for @qrErrorVerifyFailed.
  ///
  /// In en, this message translates to:
  /// **'Connection verification failed'**
  String get qrErrorVerifyFailed;

  /// No description provided for @qrErrorUnrecognized.
  ///
  /// In en, this message translates to:
  /// **'Unrecognized QR code content'**
  String get qrErrorUnrecognized;

  /// No description provided for @qrErrorRelayMissing.
  ///
  /// In en, this message translates to:
  /// **'Invalid QR code: missing panel address or node'**
  String get qrErrorRelayMissing;

  /// No description provided for @qrErrorRelaySignInFirst.
  ///
  /// In en, this message translates to:
  /// **'Sign in to this GoPanel panel first before scanning'**
  String get qrErrorRelaySignInFirst;

  /// No description provided for @qrErrorRelayConnectFailed.
  ///
  /// In en, this message translates to:
  /// **'Could not connect to this desktop via GoPanel'**
  String get qrErrorRelayConnectFailed;

  /// No description provided for @qrAlignHint.
  ///
  /// In en, this message translates to:
  /// **'Align the QR code within the frame'**
  String get qrAlignHint;

  /// No description provided for @qrVerifying.
  ///
  /// In en, this message translates to:
  /// **'Verifying and connecting…'**
  String get qrVerifying;

  /// No description provided for @serverListUnreachable.
  ///
  /// In en, this message translates to:
  /// **'Could not reach this device. Check that it is online and reachable.'**
  String get serverListUnreachable;

  /// No description provided for @serverListRelayConnectFailed.
  ///
  /// In en, this message translates to:
  /// **'Could not connect to {name} via GoPanel. Check that the desktop is online and the GoPanel sign-in is valid.'**
  String serverListRelayConnectFailed(String name);

  /// No description provided for @relayNodesTitle.
  ///
  /// In en, this message translates to:
  /// **'Hoxi Desktop computers via this GoPanel'**
  String get relayNodesTitle;

  /// No description provided for @relayNodesSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Enter the GoPanel pairing code in Hoxi Desktop Settings → Remote Control via GoPanel to see the desktop here'**
  String get relayNodesSubtitle;

  /// No description provided for @relayNodesLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load: {error}'**
  String relayNodesLoadFailed(String error);

  /// No description provided for @relayNodesEmpty.
  ///
  /// In en, this message translates to:
  /// **'No paired Hoxi Desktop computers yet'**
  String get relayNodesEmpty;

  /// No description provided for @relayNodeOnline.
  ///
  /// In en, this message translates to:
  /// **'Online'**
  String get relayNodeOnline;

  /// No description provided for @relayNodeOffline.
  ///
  /// In en, this message translates to:
  /// **'Offline'**
  String get relayNodeOffline;

  /// No description provided for @commonClose.
  ///
  /// In en, this message translates to:
  /// **'Close'**
  String get commonClose;

  /// No description provided for @containerFilterAll.
  ///
  /// In en, this message translates to:
  /// **'All'**
  String get containerFilterAll;

  /// No description provided for @nodeLinkDevicesSection.
  ///
  /// In en, this message translates to:
  /// **'Controlled Devices'**
  String get nodeLinkDevicesSection;

  /// No description provided for @codeWsFilesTitle.
  ///
  /// In en, this message translates to:
  /// **'Project files'**
  String get codeWsFilesTitle;

  /// No description provided for @codeWsFilesRoot.
  ///
  /// In en, this message translates to:
  /// **'Project root'**
  String get codeWsFilesRoot;

  /// No description provided for @codeWsFilesEmpty.
  ///
  /// In en, this message translates to:
  /// **'No viewable files in this directory'**
  String get codeWsFilesEmpty;

  /// No description provided for @codeWsFilesTruncated.
  ///
  /// In en, this message translates to:
  /// **'Only the first 500 items are shown'**
  String get codeWsFilesTruncated;

  /// No description provided for @codeWsFilesEditing.
  ///
  /// In en, this message translates to:
  /// **'Editing'**
  String get codeWsFilesEditing;

  /// No description provided for @codeWsFilesSaved.
  ///
  /// In en, this message translates to:
  /// **'Saved'**
  String get codeWsFilesSaved;

  /// No description provided for @codeWsFilesUnsaved.
  ///
  /// In en, this message translates to:
  /// **'Unsaved'**
  String get codeWsFilesUnsaved;

  /// No description provided for @codeWsFilesSave.
  ///
  /// In en, this message translates to:
  /// **'Save file'**
  String get codeWsFilesSave;

  /// No description provided for @codeWsFilesSaveSuccess.
  ///
  /// In en, this message translates to:
  /// **'File saved'**
  String get codeWsFilesSaveSuccess;

  /// No description provided for @codeWsFilesUnsavedTitle.
  ///
  /// In en, this message translates to:
  /// **'Discard unsaved changes?'**
  String get codeWsFilesUnsavedTitle;

  /// No description provided for @codeWsFilesUnsavedHint.
  ///
  /// In en, this message translates to:
  /// **'Your mobile edits will be lost when you go back.'**
  String get codeWsFilesUnsavedHint;

  /// No description provided for @codeWsFilesDiscard.
  ///
  /// In en, this message translates to:
  /// **'Discard'**
  String get codeWsFilesDiscard;

  /// No description provided for @codeWsFilesKeepEditing.
  ///
  /// In en, this message translates to:
  /// **'Keep editing'**
  String get codeWsFilesKeepEditing;

  /// No description provided for @codeWsFilesCopy.
  ///
  /// In en, this message translates to:
  /// **'Copy content'**
  String get codeWsFilesCopy;

  /// No description provided for @codeWsFilesCopied.
  ///
  /// In en, this message translates to:
  /// **'File content copied'**
  String get codeWsFilesCopied;

  /// No description provided for @codeWsFilesOperationFailed.
  ///
  /// In en, this message translates to:
  /// **'File operation failed'**
  String get codeWsFilesOperationFailed;

  /// No description provided for @codeWsActionBack.
  ///
  /// In en, this message translates to:
  /// **'Back'**
  String get codeWsActionBack;

  /// No description provided for @codeWsActionParent.
  ///
  /// In en, this message translates to:
  /// **'Parent folder'**
  String get codeWsActionParent;

  /// No description provided for @codeWsActionRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get codeWsActionRefresh;

  /// No description provided for @codeWsActionRetry.
  ///
  /// In en, this message translates to:
  /// **'Retry'**
  String get codeWsActionRetry;

  /// No description provided for @codeWsActionStop.
  ///
  /// In en, this message translates to:
  /// **'Stop execution'**
  String get codeWsActionStop;

  /// No description provided for @codeWsActionDismiss.
  ///
  /// In en, this message translates to:
  /// **'Dismiss'**
  String get codeWsActionDismiss;

  /// No description provided for @codeWsChatTitle.
  ///
  /// In en, this message translates to:
  /// **'Hoxi Development'**
  String get codeWsChatTitle;

  /// No description provided for @codeWsChatStreamReconnecting.
  ///
  /// In en, this message translates to:
  /// **'Live connection lost. Reconnecting…'**
  String get codeWsChatStreamReconnecting;

  /// No description provided for @codeWsChatRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh status'**
  String get codeWsChatRefresh;

  /// No description provided for @codeWsChatGitReview.
  ///
  /// In en, this message translates to:
  /// **'Review changes'**
  String get codeWsChatGitReview;

  /// No description provided for @codeWsChatChooseSession.
  ///
  /// In en, this message translates to:
  /// **'Choose or create session'**
  String get codeWsChatChooseSession;

  /// No description provided for @codeWsChatPreviewTitle.
  ///
  /// In en, this message translates to:
  /// **'Session previews'**
  String get codeWsChatPreviewTitle;

  /// No description provided for @codeWsChatEmptyTitle.
  ///
  /// In en, this message translates to:
  /// **'Start a development session'**
  String get codeWsChatEmptyTitle;

  /// No description provided for @codeWsChatEmptyHint.
  ///
  /// In en, this message translates to:
  /// **'Connect a project and server executor to send instructions, follow progress, open previews, and handle approvals.'**
  String get codeWsChatEmptyHint;

  /// No description provided for @codeWsChatSessionFallback.
  ///
  /// In en, this message translates to:
  /// **'Development #{id}'**
  String codeWsChatSessionFallback(int id);

  /// No description provided for @codeWsSessionSettingsTitle.
  ///
  /// In en, this message translates to:
  /// **'Session settings'**
  String get codeWsSessionSettingsTitle;

  /// No description provided for @codeWsSessionSettingsName.
  ///
  /// In en, this message translates to:
  /// **'Session name'**
  String get codeWsSessionSettingsName;

  /// No description provided for @codeWsSessionSettingsApproval.
  ///
  /// In en, this message translates to:
  /// **'Approval policy'**
  String get codeWsSessionSettingsApproval;

  /// No description provided for @codeWsSessionSettingsPolicyManual.
  ///
  /// In en, this message translates to:
  /// **'Confirm each time'**
  String get codeWsSessionSettingsPolicyManual;

  /// No description provided for @codeWsSessionSettingsPolicySafeAuto.
  ///
  /// In en, this message translates to:
  /// **'Safe auto'**
  String get codeWsSessionSettingsPolicySafeAuto;

  /// No description provided for @codeWsSessionSettingsPolicyFullAuto.
  ///
  /// In en, this message translates to:
  /// **'Full auto'**
  String get codeWsSessionSettingsPolicyFullAuto;

  /// No description provided for @codeWsSessionSettingsPolicyRejectsInConversation.
  ///
  /// In en, this message translates to:
  /// **'{executor} cannot ask you in conversation mode: actions that need permission (such as running commands) are rejected outright, so the AI may not finish the task. Approve them one by one in terminal mode instead.'**
  String codeWsSessionSettingsPolicyRejectsInConversation(String executor);

  /// No description provided for @codeWsSessionSettingsModel.
  ///
  /// In en, this message translates to:
  /// **'Model'**
  String get codeWsSessionSettingsModel;

  /// No description provided for @codeWsSessionSettingsModelHint.
  ///
  /// In en, this message translates to:
  /// **'Leave empty for the executor default'**
  String get codeWsSessionSettingsModelHint;

  /// No description provided for @codeWsSessionSettingsEffort.
  ///
  /// In en, this message translates to:
  /// **'Reasoning effort'**
  String get codeWsSessionSettingsEffort;

  /// No description provided for @codeWsSessionSettingsEffortDefault.
  ///
  /// In en, this message translates to:
  /// **'Default'**
  String get codeWsSessionSettingsEffortDefault;

  /// No description provided for @codeWsSessionSettingsSave.
  ///
  /// In en, this message translates to:
  /// **'Save settings'**
  String get codeWsSessionSettingsSave;

  /// No description provided for @codeWsSessionSettingsSaved.
  ///
  /// In en, this message translates to:
  /// **'Session settings saved'**
  String get codeWsSessionSettingsSaved;

  /// No description provided for @codeWsAttentionTitle.
  ///
  /// In en, this message translates to:
  /// **'Needs attention'**
  String get codeWsAttentionTitle;

  /// No description provided for @codeWsAttentionViewAll.
  ///
  /// In en, this message translates to:
  /// **'View all'**
  String get codeWsAttentionViewAll;

  /// No description provided for @codeWsAttentionEmpty.
  ///
  /// In en, this message translates to:
  /// **'No Code items need your attention'**
  String get codeWsAttentionEmpty;

  /// No description provided for @codeWsAttentionLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load attention items'**
  String get codeWsAttentionLoadFailed;

  /// No description provided for @codeWsAttentionSession.
  ///
  /// In en, this message translates to:
  /// **'Development session #{id}'**
  String codeWsAttentionSession(int id);

  /// No description provided for @codeWsAttentionConfirmHint.
  ///
  /// In en, this message translates to:
  /// **'This action immediately affects the development session. Continue?'**
  String get codeWsAttentionConfirmHint;

  /// No description provided for @codeWsAttentionCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get codeWsAttentionCancel;

  /// No description provided for @codeWsAttentionConfirm.
  ///
  /// In en, this message translates to:
  /// **'Confirm'**
  String get codeWsAttentionConfirm;

  /// No description provided for @codeWsAttentionActionSuccess.
  ///
  /// In en, this message translates to:
  /// **'Action completed'**
  String get codeWsAttentionActionSuccess;

  /// No description provided for @codeWsAttentionActionFailed.
  ///
  /// In en, this message translates to:
  /// **'Action failed. Try again later.'**
  String get codeWsAttentionActionFailed;

  /// No description provided for @codeWsAttentionOpenFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to open the development session'**
  String get codeWsAttentionOpenFailed;

  /// No description provided for @codeWsInstructionClosedHint.
  ///
  /// In en, this message translates to:
  /// **'This session is being delivered and cannot accept more changes'**
  String get codeWsInstructionClosedHint;

  /// No description provided for @codeWsInstructionInputHint.
  ///
  /// In en, this message translates to:
  /// **'Enter an instruction or follow-up...'**
  String get codeWsInstructionInputHint;

  /// No description provided for @codeWsInstructionSelectSessionHint.
  ///
  /// In en, this message translates to:
  /// **'Select a development session first'**
  String get codeWsInstructionSelectSessionHint;

  /// No description provided for @codeWsInstructionSend.
  ///
  /// In en, this message translates to:
  /// **'Send instruction'**
  String get codeWsInstructionSend;

  /// No description provided for @codeWsHubTitle.
  ///
  /// In en, this message translates to:
  /// **'Development command center'**
  String get codeWsHubTitle;

  /// No description provided for @codeWsHubRecentTasks.
  ///
  /// In en, this message translates to:
  /// **'Recent'**
  String get codeWsHubRecentTasks;

  /// No description provided for @codeWsHubProjectsTab.
  ///
  /// In en, this message translates to:
  /// **'Projects'**
  String get codeWsHubProjectsTab;

  /// No description provided for @codeWsHubSessionCount.
  ///
  /// In en, this message translates to:
  /// **'{count}'**
  String codeWsHubSessionCount(int count);

  /// No description provided for @codeWsHubProject.
  ///
  /// In en, this message translates to:
  /// **'Current project'**
  String get codeWsHubProject;

  /// No description provided for @codeWsHubNoProjectTasks.
  ///
  /// In en, this message translates to:
  /// **'No tasks'**
  String get codeWsHubNoProjectTasks;

  /// No description provided for @codeWsHubNoProjectTasksHint.
  ///
  /// In en, this message translates to:
  /// **'Create a session to add a task here.'**
  String get codeWsHubNoProjectTasksHint;

  /// No description provided for @codeWsHubCreate.
  ///
  /// In en, this message translates to:
  /// **'New'**
  String get codeWsHubCreate;

  /// No description provided for @codeWsHubProjectTerminal.
  ///
  /// In en, this message translates to:
  /// **'Project terminal'**
  String get codeWsHubProjectTerminal;

  /// No description provided for @codeWsHubProjectTerminalHint.
  ///
  /// In en, this message translates to:
  /// **'Open the project directory for command-line work and quick diagnostics.'**
  String get codeWsHubProjectTerminalHint;

  /// No description provided for @codeWsTerminalChooseProject.
  ///
  /// In en, this message translates to:
  /// **'Choose a project'**
  String get codeWsTerminalChooseProject;

  /// No description provided for @codeWsTerminalChooseProjectHint.
  ///
  /// In en, this message translates to:
  /// **'The terminal starts in the project directory. Review high-risk commands carefully.'**
  String get codeWsTerminalChooseProjectHint;

  /// No description provided for @codeWsTerminalNoProjects.
  ///
  /// In en, this message translates to:
  /// **'No development projects yet. Create one in the admin console first.'**
  String get codeWsTerminalNoProjects;

  /// No description provided for @codeWsTerminalOpenFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to open project terminal'**
  String get codeWsTerminalOpenFailed;

  /// No description provided for @codeWsTerminalOpening.
  ///
  /// In en, this message translates to:
  /// **'Opening terminal...'**
  String get codeWsTerminalOpening;

  /// No description provided for @codeWsTerminalProjectTitle.
  ///
  /// In en, this message translates to:
  /// **'Project terminal'**
  String get codeWsTerminalProjectTitle;

  /// No description provided for @codeWsTerminalNoTask.
  ///
  /// In en, this message translates to:
  /// **'No task'**
  String get codeWsTerminalNoTask;

  /// No description provided for @codeWsTerminalTaskTitle.
  ///
  /// In en, this message translates to:
  /// **'Task · {task}'**
  String codeWsTerminalTaskTitle(String task);

  /// No description provided for @codeWsTerminalReconnect.
  ///
  /// In en, this message translates to:
  /// **'Reconnect'**
  String get codeWsTerminalReconnect;

  /// No description provided for @codeWsTerminalConnecting.
  ///
  /// In en, this message translates to:
  /// **'Connecting to terminal…'**
  String get codeWsTerminalConnecting;

  /// No description provided for @codeWsTerminalReconnecting.
  ///
  /// In en, this message translates to:
  /// **'Connection lost. Recovering…'**
  String get codeWsTerminalReconnecting;

  /// No description provided for @codeWsTerminalConnectionFailed.
  ///
  /// In en, this message translates to:
  /// **'Terminal connection failed. Check the network and retry.'**
  String get codeWsTerminalConnectionFailed;

  /// No description provided for @codeWsTerminalSessionClosed.
  ///
  /// In en, this message translates to:
  /// **'Terminal session ended'**
  String get codeWsTerminalSessionClosed;

  /// No description provided for @codeWsTerminalRelayUnsupported.
  ///
  /// In en, this message translates to:
  /// **'Interactive terminals are unavailable over the GoPanel tunnel. Use a Hoxi desktop node on the local network instead.'**
  String get codeWsTerminalRelayUnsupported;

  /// No description provided for @codeWsTerminalConnected.
  ///
  /// In en, this message translates to:
  /// **'Connected'**
  String get codeWsTerminalConnected;

  /// No description provided for @codeWsTerminalReleaseControl.
  ///
  /// In en, this message translates to:
  /// **'Release terminal control'**
  String get codeWsTerminalReleaseControl;

  /// No description provided for @codeWsTerminalTakeControl.
  ///
  /// In en, this message translates to:
  /// **'Take terminal control'**
  String get codeWsTerminalTakeControl;

  /// No description provided for @codeWsTerminalSessionStatus.
  ///
  /// In en, this message translates to:
  /// **'Session status and instructions'**
  String get codeWsTerminalSessionStatus;

  /// No description provided for @codeWsTerminalFiles.
  ///
  /// In en, this message translates to:
  /// **'Files'**
  String get codeWsTerminalFiles;

  /// No description provided for @codeWsTerminalCommandHint.
  ///
  /// In en, this message translates to:
  /// **'Type a command; long and composed input supported'**
  String get codeWsTerminalCommandHint;

  /// No description provided for @codeWsTerminalDisconnected.
  ///
  /// In en, this message translates to:
  /// **'Terminal disconnected'**
  String get codeWsTerminalDisconnected;

  /// No description provided for @codeWsTerminalTakeControlHint.
  ///
  /// In en, this message translates to:
  /// **'Tap the input to take terminal control'**
  String get codeWsTerminalTakeControlHint;

  /// No description provided for @codeWsTerminalSendCommand.
  ///
  /// In en, this message translates to:
  /// **'Send command'**
  String get codeWsTerminalSendCommand;

  /// No description provided for @codeWsTerminalHistoryPrevious.
  ///
  /// In en, this message translates to:
  /// **'↑ History'**
  String get codeWsTerminalHistoryPrevious;

  /// No description provided for @codeWsTerminalHistoryNext.
  ///
  /// In en, this message translates to:
  /// **'↓ History'**
  String get codeWsTerminalHistoryNext;

  /// No description provided for @codeWsTerminalPaste.
  ///
  /// In en, this message translates to:
  /// **'Paste'**
  String get codeWsTerminalPaste;

  /// No description provided for @codeWsTerminalNativeKeyboard.
  ///
  /// In en, this message translates to:
  /// **'Terminal keyboard'**
  String get codeWsTerminalNativeKeyboard;

  /// No description provided for @codeWsSessionIsolateLabel.
  ///
  /// In en, this message translates to:
  /// **'Isolate workspace'**
  String get codeWsSessionIsolateLabel;

  /// No description provided for @codeWsSessionIsolateNo.
  ///
  /// In en, this message translates to:
  /// **'No'**
  String get codeWsSessionIsolateNo;

  /// No description provided for @codeWsSessionIsolateYes.
  ///
  /// In en, this message translates to:
  /// **'Yes'**
  String get codeWsSessionIsolateYes;

  /// No description provided for @codeWsSessionWorktreeChecking.
  ///
  /// In en, this message translates to:
  /// **'Checking workspace isolation...'**
  String get codeWsSessionWorktreeChecking;

  /// No description provided for @codeWsSessionWorktreeReady.
  ///
  /// In en, this message translates to:
  /// **'Isolated workspace enabled'**
  String get codeWsSessionWorktreeReady;

  /// No description provided for @codeWsSessionWorktreeReadyHint.
  ///
  /// In en, this message translates to:
  /// **'This session runs in a separate Git Worktree without editing the project directory directly.'**
  String get codeWsSessionWorktreeReadyHint;

  /// No description provided for @codeWsSessionWorktreeDirty.
  ///
  /// In en, this message translates to:
  /// **'Uncommitted changes detected'**
  String get codeWsSessionWorktreeDirty;

  /// No description provided for @codeWsSessionWorktreeDirtyHint.
  ///
  /// In en, this message translates to:
  /// **'Resolve these repositories on desktop before creating a session: {repositories}'**
  String codeWsSessionWorktreeDirtyHint(String repositories);

  /// No description provided for @codeWsSessionWorktreeUnavailable.
  ///
  /// In en, this message translates to:
  /// **'This project cannot create a safe session'**
  String get codeWsSessionWorktreeUnavailable;

  /// No description provided for @codeWsSessionWorktreeReasonNotGit.
  ///
  /// In en, this message translates to:
  /// **'The project directory is not a Git repository. Configure it on desktop first.'**
  String get codeWsSessionWorktreeReasonNotGit;

  /// No description provided for @codeWsSessionWorktreeReasonSourceUnavailable.
  ///
  /// In en, this message translates to:
  /// **'The project source directory is unavailable. Check its desktop configuration.'**
  String get codeWsSessionWorktreeReasonSourceUnavailable;

  /// No description provided for @codeWsSessionWorktreeReasonUnknown.
  ///
  /// In en, this message translates to:
  /// **'Git Worktree isolation is unavailable for this project.'**
  String get codeWsSessionWorktreeReasonUnknown;

  /// No description provided for @codeWsSessionWorktreeCheckFailed.
  ///
  /// In en, this message translates to:
  /// **'Workspace isolation check failed'**
  String get codeWsSessionWorktreeCheckFailed;

  /// No description provided for @codeWsSessionWorktreeRetry.
  ///
  /// In en, this message translates to:
  /// **'Check again'**
  String get codeWsSessionWorktreeRetry;

  /// No description provided for @codeWsStopTitle.
  ///
  /// In en, this message translates to:
  /// **'Stop the current Code task?'**
  String get codeWsStopTitle;

  /// No description provided for @codeWsStopDescription.
  ///
  /// In en, this message translates to:
  /// **'Running and queued instructions will stop. Existing file changes are preserved.'**
  String get codeWsStopDescription;

  /// No description provided for @codeWsStopCancel.
  ///
  /// In en, this message translates to:
  /// **'Keep running'**
  String get codeWsStopCancel;

  /// No description provided for @codeWsStopConfirm.
  ///
  /// In en, this message translates to:
  /// **'Stop'**
  String get codeWsStopConfirm;

  /// No description provided for @codeWsStopSuccess.
  ///
  /// In en, this message translates to:
  /// **'Stop request sent'**
  String get codeWsStopSuccess;

  /// No description provided for @codeWsRecoveryTitle.
  ///
  /// In en, this message translates to:
  /// **'Session history and recovery'**
  String get codeWsRecoveryTitle;

  /// No description provided for @codeWsRecoveryInitialization.
  ///
  /// In en, this message translates to:
  /// **'Session initialization'**
  String get codeWsRecoveryInitialization;

  /// No description provided for @codeWsRecoveryInitializationFailed.
  ///
  /// In en, this message translates to:
  /// **'Initialization failed'**
  String get codeWsRecoveryInitializationFailed;

  /// No description provided for @codeWsRecoveryInitializationFailedHint.
  ///
  /// In en, this message translates to:
  /// **'The session environment failed to initialize. Retry or review the error details.'**
  String get codeWsRecoveryInitializationFailedHint;

  /// No description provided for @codeWsRecoveryInitializing.
  ///
  /// In en, this message translates to:
  /// **'Initializing'**
  String get codeWsRecoveryInitializing;

  /// No description provided for @codeWsRecoveryInitialized.
  ///
  /// In en, this message translates to:
  /// **'Initialized'**
  String get codeWsRecoveryInitialized;

  /// No description provided for @codeWsRecoveryInitializingHint.
  ///
  /// In en, this message translates to:
  /// **'Preparing the isolated workspace and execution environment.'**
  String get codeWsRecoveryInitializingHint;

  /// No description provided for @codeWsRecoveryInitializedHint.
  ///
  /// In en, this message translates to:
  /// **'The execution environment is ready for more instructions.'**
  String get codeWsRecoveryInitializedHint;

  /// No description provided for @codeWsRecoveryRetryInitialization.
  ///
  /// In en, this message translates to:
  /// **'Retry initialization'**
  String get codeWsRecoveryRetryInitialization;

  /// No description provided for @codeWsRecoveryRetryInitializationHint.
  ///
  /// In en, this message translates to:
  /// **'Hoxi will prepare this session workspace and execution environment again. Continue?'**
  String get codeWsRecoveryRetryInitializationHint;

  /// No description provided for @codeWsRecoveryConversation.
  ///
  /// In en, this message translates to:
  /// **'Full conversation'**
  String get codeWsRecoveryConversation;

  /// No description provided for @codeWsRecoveryNoMessages.
  ///
  /// In en, this message translates to:
  /// **'No historical messages in this session'**
  String get codeWsRecoveryNoMessages;

  /// No description provided for @codeWsRecoveryRuns.
  ///
  /// In en, this message translates to:
  /// **'Execution runs'**
  String get codeWsRecoveryRuns;

  /// No description provided for @codeWsRecoveryNoRuns.
  ///
  /// In en, this message translates to:
  /// **'No execution runs in this session'**
  String get codeWsRecoveryNoRuns;

  /// No description provided for @codeWsRecoveryViewRunDetail.
  ///
  /// In en, this message translates to:
  /// **'View details'**
  String get codeWsRecoveryViewRunDetail;

  /// No description provided for @codeWsRecoveryLoadMore.
  ///
  /// In en, this message translates to:
  /// **'Load more'**
  String get codeWsRecoveryLoadMore;

  /// No description provided for @codeWsRecoveryUser.
  ///
  /// In en, this message translates to:
  /// **'You'**
  String get codeWsRecoveryUser;

  /// No description provided for @codeWsRecoveryAgent.
  ///
  /// In en, this message translates to:
  /// **'Code assistant'**
  String get codeWsRecoveryAgent;

  /// No description provided for @codeWsRecoveryDuration.
  ///
  /// In en, this message translates to:
  /// **'{duration} ms'**
  String codeWsRecoveryDuration(int duration);

  /// No description provided for @codeWsRecoveryTokens.
  ///
  /// In en, this message translates to:
  /// **'{tokens} tokens'**
  String codeWsRecoveryTokens(int tokens);

  /// No description provided for @codeWsRecoveryRetryInstruction.
  ///
  /// In en, this message translates to:
  /// **'Retry instruction'**
  String get codeWsRecoveryRetryInstruction;

  /// No description provided for @codeWsRecoveryRetryInstructionHint.
  ///
  /// In en, this message translates to:
  /// **'This failed or stopped instruction will run again. Continue?'**
  String get codeWsRecoveryRetryInstructionHint;

  /// No description provided for @codeWsRecoveryRetryQueued.
  ///
  /// In en, this message translates to:
  /// **'Queued again'**
  String get codeWsRecoveryRetryQueued;

  /// No description provided for @codeWsRecoveryStatusCompleted.
  ///
  /// In en, this message translates to:
  /// **'Completed'**
  String get codeWsRecoveryStatusCompleted;

  /// No description provided for @codeWsRecoveryStatusFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed'**
  String get codeWsRecoveryStatusFailed;

  /// No description provided for @codeWsRecoveryStatusStopped.
  ///
  /// In en, this message translates to:
  /// **'Stopped'**
  String get codeWsRecoveryStatusStopped;

  /// No description provided for @codeWsRecoveryStatusRunning.
  ///
  /// In en, this message translates to:
  /// **'Running'**
  String get codeWsRecoveryStatusRunning;

  /// No description provided for @codeWsRecoveryStatusAwaitingApproval.
  ///
  /// In en, this message translates to:
  /// **'Awaiting approval'**
  String get codeWsRecoveryStatusAwaitingApproval;

  /// No description provided for @codeWsRecoveryStatusQueued.
  ///
  /// In en, this message translates to:
  /// **'Queued'**
  String get codeWsRecoveryStatusQueued;

  /// No description provided for @codeWsRecoveryStatusUnknown.
  ///
  /// In en, this message translates to:
  /// **'Unknown'**
  String get codeWsRecoveryStatusUnknown;

  /// No description provided for @codeWsRecoveryLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load session history'**
  String get codeWsRecoveryLoadFailed;

  /// No description provided for @codeWsRecoveryCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get codeWsRecoveryCancel;

  /// No description provided for @codeWsRecoveryConfirm.
  ///
  /// In en, this message translates to:
  /// **'Confirm'**
  String get codeWsRecoveryConfirm;

  /// No description provided for @codeWsRecoveryActionSuccess.
  ///
  /// In en, this message translates to:
  /// **'Action submitted'**
  String get codeWsRecoveryActionSuccess;

  /// No description provided for @codeWsRecoveryActionFailed.
  ///
  /// In en, this message translates to:
  /// **'Action failed. Try again later.'**
  String get codeWsRecoveryActionFailed;

  /// No description provided for @codeWsRunDetailTitle.
  ///
  /// In en, this message translates to:
  /// **'Run #{id}'**
  String codeWsRunDetailTitle(int id);

  /// No description provided for @codeWsRunDetailCopy.
  ///
  /// In en, this message translates to:
  /// **'Copy diagnostics'**
  String get codeWsRunDetailCopy;

  /// No description provided for @codeWsRunDetailCopied.
  ///
  /// In en, this message translates to:
  /// **'Diagnostics copied'**
  String get codeWsRunDetailCopied;

  /// No description provided for @codeWsRunDetailLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load run details'**
  String get codeWsRunDetailLoadFailed;

  /// No description provided for @codeWsRunDetailEmpty.
  ///
  /// In en, this message translates to:
  /// **'No run details available'**
  String get codeWsRunDetailEmpty;

  /// No description provided for @codeWsRunDetailOverview.
  ///
  /// In en, this message translates to:
  /// **'Run overview'**
  String get codeWsRunDetailOverview;

  /// No description provided for @codeWsRunDetailStatus.
  ///
  /// In en, this message translates to:
  /// **'Status'**
  String get codeWsRunDetailStatus;

  /// No description provided for @codeWsRunDetailExecutor.
  ///
  /// In en, this message translates to:
  /// **'Executor'**
  String get codeWsRunDetailExecutor;

  /// No description provided for @codeWsRunDetailModel.
  ///
  /// In en, this message translates to:
  /// **'Model'**
  String get codeWsRunDetailModel;

  /// No description provided for @codeWsRunDetailExitCode.
  ///
  /// In en, this message translates to:
  /// **'Exit code'**
  String get codeWsRunDetailExitCode;

  /// No description provided for @codeWsRunDetailDurationLabel.
  ///
  /// In en, this message translates to:
  /// **'Duration'**
  String get codeWsRunDetailDurationLabel;

  /// No description provided for @codeWsRunDetailSession.
  ///
  /// In en, this message translates to:
  /// **'Session'**
  String get codeWsRunDetailSession;

  /// No description provided for @codeWsRunDetailTask.
  ///
  /// In en, this message translates to:
  /// **'Task'**
  String get codeWsRunDetailTask;

  /// No description provided for @codeWsRunDetailInstruction.
  ///
  /// In en, this message translates to:
  /// **'Instruction'**
  String get codeWsRunDetailInstruction;

  /// No description provided for @codeWsRunDetailStartedAt.
  ///
  /// In en, this message translates to:
  /// **'Started'**
  String get codeWsRunDetailStartedAt;

  /// No description provided for @codeWsRunDetailCompletedAt.
  ///
  /// In en, this message translates to:
  /// **'Completed'**
  String get codeWsRunDetailCompletedAt;

  /// No description provided for @codeWsRunDetailInputTokens.
  ///
  /// In en, this message translates to:
  /// **'Input tokens'**
  String get codeWsRunDetailInputTokens;

  /// No description provided for @codeWsRunDetailOutputTokens.
  ///
  /// In en, this message translates to:
  /// **'Output tokens'**
  String get codeWsRunDetailOutputTokens;

  /// No description provided for @codeWsRunDetailCachedTokens.
  ///
  /// In en, this message translates to:
  /// **'Cached input tokens'**
  String get codeWsRunDetailCachedTokens;

  /// No description provided for @codeWsRunDetailReasoningTokens.
  ///
  /// In en, this message translates to:
  /// **'Reasoning tokens'**
  String get codeWsRunDetailReasoningTokens;

  /// No description provided for @codeWsRunDetailTotalTokens.
  ///
  /// In en, this message translates to:
  /// **'Total tokens'**
  String get codeWsRunDetailTotalTokens;

  /// No description provided for @codeWsRunDetailPrompt.
  ///
  /// In en, this message translates to:
  /// **'Original instruction'**
  String get codeWsRunDetailPrompt;

  /// No description provided for @codeWsRunDetailError.
  ///
  /// In en, this message translates to:
  /// **'Error details'**
  String get codeWsRunDetailError;

  /// No description provided for @codeWsRunDetailOutput.
  ///
  /// In en, this message translates to:
  /// **'Execution output'**
  String get codeWsRunDetailOutput;

  /// No description provided for @codeWsRunDetailRawOutput.
  ///
  /// In en, this message translates to:
  /// **'Raw logs'**
  String get codeWsRunDetailRawOutput;

  /// No description provided for @codeWsRunDetailNoPrompt.
  ///
  /// In en, this message translates to:
  /// **'No original instruction recorded'**
  String get codeWsRunDetailNoPrompt;

  /// No description provided for @codeWsRunDetailNoOutput.
  ///
  /// In en, this message translates to:
  /// **'No structured execution output'**
  String get codeWsRunDetailNoOutput;

  /// No description provided for @codeWsRunDetailNoRawOutput.
  ///
  /// In en, this message translates to:
  /// **'No raw logs available'**
  String get codeWsRunDetailNoRawOutput;

  /// No description provided for @codeWsDeliveryTitle.
  ///
  /// In en, this message translates to:
  /// **'Unified delivery'**
  String get codeWsDeliveryTitle;

  /// No description provided for @codeWsDeliveryEmpty.
  ///
  /// In en, this message translates to:
  /// **'Merge this session into the project branch and push it through the managed delivery flow.'**
  String get codeWsDeliveryEmpty;

  /// No description provided for @codeWsDeliveryUnavailable.
  ///
  /// In en, this message translates to:
  /// **'This session has no managed worktree. Create a project session with Git Worktree support.'**
  String get codeWsDeliveryUnavailable;

  /// No description provided for @codeWsDeliveryStart.
  ///
  /// In en, this message translates to:
  /// **'Start delivery'**
  String get codeWsDeliveryStart;

  /// No description provided for @codeWsDeliveryRetry.
  ///
  /// In en, this message translates to:
  /// **'Retry delivery'**
  String get codeWsDeliveryRetry;

  /// No description provided for @codeWsDeliveryDeliverLatest.
  ///
  /// In en, this message translates to:
  /// **'Deliver newer commits'**
  String get codeWsDeliveryDeliverLatest;

  /// No description provided for @codeWsDeliveryConfirmTitle.
  ///
  /// In en, this message translates to:
  /// **'Start unified delivery?'**
  String get codeWsDeliveryConfirmTitle;

  /// No description provided for @codeWsDeliveryConfirmDescription.
  ///
  /// In en, this message translates to:
  /// **'Hoxi snapshots the saved commits, then syncs, merges, runs quality checks, and pushes. Newer commits remain available for another delivery.'**
  String get codeWsDeliveryConfirmDescription;

  /// No description provided for @codeWsDeliveryCancel.
  ///
  /// In en, this message translates to:
  /// **'Not now'**
  String get codeWsDeliveryCancel;

  /// No description provided for @codeWsDeliveryConfirm.
  ///
  /// In en, this message translates to:
  /// **'Deliver'**
  String get codeWsDeliveryConfirm;

  /// No description provided for @codeWsDeliveryReviewSummary.
  ///
  /// In en, this message translates to:
  /// **'This delivery covers {files} files (+{additions} −{deletions})'**
  String codeWsDeliveryReviewSummary(int files, int additions, int deletions);

  /// No description provided for @codeWsDeliveryReviewNotReady.
  ///
  /// In en, this message translates to:
  /// **'There are no deliverable commits yet, or some changes are still unsaved. Save them before delivering.'**
  String get codeWsDeliveryReviewNotReady;

  /// No description provided for @codeWsDeliveryStarted.
  ///
  /// In en, this message translates to:
  /// **'Delivery queued'**
  String get codeWsDeliveryStarted;

  /// No description provided for @codeWsDeliveryFailed.
  ///
  /// In en, this message translates to:
  /// **'Delivery failed'**
  String get codeWsDeliveryFailed;

  /// No description provided for @codeWsDeliveryConflict.
  ///
  /// In en, this message translates to:
  /// **'Merge conflicts detected'**
  String get codeWsDeliveryConflict;

  /// No description provided for @codeWsDeliveryConflictHint.
  ///
  /// In en, this message translates to:
  /// **'Open this session worktree on desktop, resolve and commit these conflicts, then retry here.'**
  String get codeWsDeliveryConflictHint;

  /// No description provided for @codeWsDeliveryCompleted.
  ///
  /// In en, this message translates to:
  /// **'Merge and push completed'**
  String get codeWsDeliveryCompleted;

  /// No description provided for @codeWsDeliveryPending.
  ///
  /// In en, this message translates to:
  /// **'More commits pending'**
  String get codeWsDeliveryPending;

  /// No description provided for @codeWsDeliveryCompletedWithPending.
  ///
  /// In en, this message translates to:
  /// **'This batch completed; newer commits are waiting for delivery'**
  String get codeWsDeliveryCompletedWithPending;

  /// No description provided for @codeWsDeliveryCompletedWithUnsaved.
  ///
  /// In en, this message translates to:
  /// **'This batch completed; save the newer working tree changes on desktop first'**
  String get codeWsDeliveryCompletedWithUnsaved;

  /// No description provided for @codeWsDeliveryQueued.
  ///
  /// In en, this message translates to:
  /// **'Waiting for delivery'**
  String get codeWsDeliveryQueued;

  /// No description provided for @codeWsDeliveryRunning.
  ///
  /// In en, this message translates to:
  /// **'Delivering'**
  String get codeWsDeliveryRunning;

  /// No description provided for @codeWsDeliveryQueuePosition.
  ///
  /// In en, this message translates to:
  /// **'Queue position {position}'**
  String codeWsDeliveryQueuePosition(int position);

  /// No description provided for @codeWsDeliveryAttempt.
  ///
  /// In en, this message translates to:
  /// **'Attempt {attempt}'**
  String codeWsDeliveryAttempt(int attempt);

  /// No description provided for @codeWsDeliveryStageStoppingTerminal.
  ///
  /// In en, this message translates to:
  /// **'Stopping host terminals'**
  String get codeWsDeliveryStageStoppingTerminal;

  /// No description provided for @codeWsDeliveryStageSyncing.
  ///
  /// In en, this message translates to:
  /// **'Syncing the remote branch'**
  String get codeWsDeliveryStageSyncing;

  /// No description provided for @codeWsDeliveryStageMerging.
  ///
  /// In en, this message translates to:
  /// **'Merging session changes'**
  String get codeWsDeliveryStageMerging;

  /// No description provided for @codeWsDeliveryStageQualityCheck.
  ///
  /// In en, this message translates to:
  /// **'Running quality checks'**
  String get codeWsDeliveryStageQualityCheck;

  /// No description provided for @codeWsDeliveryStagePushing.
  ///
  /// In en, this message translates to:
  /// **'Pushing the main branch'**
  String get codeWsDeliveryStagePushing;

  /// No description provided for @codeWsDeliveryStageVerifying.
  ///
  /// In en, this message translates to:
  /// **'Verifying the remote commit'**
  String get codeWsDeliveryStageVerifying;

  /// No description provided for @codeWsDeliveryStageCleaning.
  ///
  /// In en, this message translates to:
  /// **'Cleaning the session workspace'**
  String get codeWsDeliveryStageCleaning;

  /// No description provided for @codeWsQualityTitle.
  ///
  /// In en, this message translates to:
  /// **'Quality Gate'**
  String get codeWsQualityTitle;

  /// No description provided for @codeWsQualityRun.
  ///
  /// In en, this message translates to:
  /// **'Run'**
  String get codeWsQualityRun;

  /// No description provided for @codeWsQualityRunAll.
  ///
  /// In en, this message translates to:
  /// **'Run All'**
  String get codeWsQualityRunAll;

  /// No description provided for @codeWsQualityRunning.
  ///
  /// In en, this message translates to:
  /// **'Running checks...'**
  String get codeWsQualityRunning;

  /// No description provided for @codeWsQualityEmpty.
  ///
  /// In en, this message translates to:
  /// **'No quality check commands detected'**
  String get codeWsQualityEmpty;

  /// No description provided for @codeWsQualityEmptyHint.
  ///
  /// In en, this message translates to:
  /// **'You can add test or lint scripts to package.json or Makefile.'**
  String get codeWsQualityEmptyHint;

  /// No description provided for @codeWsQualityPassed.
  ///
  /// In en, this message translates to:
  /// **'Passed'**
  String get codeWsQualityPassed;

  /// No description provided for @codeWsQualityFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed'**
  String get codeWsQualityFailed;

  /// No description provided for @codeWsQualityPending.
  ///
  /// In en, this message translates to:
  /// **'Not run'**
  String get codeWsQualityPending;

  /// No description provided for @codeWsQualityExitCode.
  ///
  /// In en, this message translates to:
  /// **'Exit code {code}'**
  String codeWsQualityExitCode(int code);

  /// No description provided for @codeWsQualityDuration.
  ///
  /// In en, this message translates to:
  /// **'Duration {duration} ms'**
  String codeWsQualityDuration(int duration);

  /// No description provided for @codeWsQualityLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load quality checks'**
  String get codeWsQualityLoadFailed;

  /// No description provided for @codeWsQualityRunFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to run quality check'**
  String get codeWsQualityRunFailed;

  /// No description provided for @codeWsMemoryExtract.
  ///
  /// In en, this message translates to:
  /// **'Extract Project Memory'**
  String get codeWsMemoryExtract;

  /// No description provided for @codeWsMemoryExtracting.
  ///
  /// In en, this message translates to:
  /// **'Extracting session memory...'**
  String get codeWsMemoryExtracting;

  /// No description provided for @codeWsMemoryExtractSuccess.
  ///
  /// In en, this message translates to:
  /// **'Memory extraction queued; will persist as long-term rules upon analysis'**
  String get codeWsMemoryExtractSuccess;

  /// No description provided for @codeWsMemoryExtractFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to queue memory extraction'**
  String get codeWsMemoryExtractFailed;

  /// No description provided for @codeWsRecoveryAbortInitialization.
  ///
  /// In en, this message translates to:
  /// **'Abort initialization?'**
  String get codeWsRecoveryAbortInitialization;

  /// No description provided for @codeWsRecoveryAbortInitializationHint.
  ///
  /// In en, this message translates to:
  /// **'This stops the current initialization and keeps the existing session record.'**
  String get codeWsRecoveryAbortInitializationHint;

  /// No description provided for @gitReviewTitle.
  ///
  /// In en, this message translates to:
  /// **'Review changes'**
  String get gitReviewTitle;

  /// No description provided for @gitReviewSummary.
  ///
  /// In en, this message translates to:
  /// **'{files} files'**
  String gitReviewSummary(int files);

  /// No description provided for @gitReviewNoRepository.
  ///
  /// In en, this message translates to:
  /// **'No Git repository is available for this session'**
  String get gitReviewNoRepository;

  /// No description provided for @gitReviewNoChanges.
  ///
  /// In en, this message translates to:
  /// **'The workspace has no unsaved changes'**
  String get gitReviewNoChanges;

  /// No description provided for @gitReviewLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load Git changes'**
  String get gitReviewLoadFailed;

  /// No description provided for @gitReviewDetached.
  ///
  /// In en, this message translates to:
  /// **'Detached HEAD'**
  String get gitReviewDetached;

  /// No description provided for @gitReviewStaged.
  ///
  /// In en, this message translates to:
  /// **'Staged'**
  String get gitReviewStaged;

  /// No description provided for @gitReviewChanged.
  ///
  /// In en, this message translates to:
  /// **'Unstaged'**
  String get gitReviewChanged;

  /// No description provided for @gitReviewUntracked.
  ///
  /// In en, this message translates to:
  /// **'New file'**
  String get gitReviewUntracked;

  /// No description provided for @gitReviewSavedCommits.
  ///
  /// In en, this message translates to:
  /// **'{count} saved commits'**
  String gitReviewSavedCommits(int count);

  /// No description provided for @gitReviewTruncated.
  ///
  /// In en, this message translates to:
  /// **'Only the first 500 changed files are shown'**
  String get gitReviewTruncated;

  /// No description provided for @gitReviewWorkingDiff.
  ///
  /// In en, this message translates to:
  /// **'View unstaged diff'**
  String get gitReviewWorkingDiff;

  /// No description provided for @gitReviewStagedDiff.
  ///
  /// In en, this message translates to:
  /// **'View staged diff'**
  String get gitReviewStagedDiff;

  /// No description provided for @gitReviewDiffTitle.
  ///
  /// In en, this message translates to:
  /// **'File diff'**
  String get gitReviewDiffTitle;

  /// No description provided for @gitReviewDiffLoadFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to load file diff'**
  String get gitReviewDiffLoadFailed;

  /// No description provided for @gitReviewDiffEmpty.
  ///
  /// In en, this message translates to:
  /// **'No text diff is available for this file'**
  String get gitReviewDiffEmpty;

  /// No description provided for @gitReviewDiffTruncated.
  ///
  /// In en, this message translates to:
  /// **'The diff is too large; only the first 1 MB is shown'**
  String get gitReviewDiffTruncated;

  /// No description provided for @gitReviewCopyDiff.
  ///
  /// In en, this message translates to:
  /// **'Copy diff'**
  String get gitReviewCopyDiff;

  /// No description provided for @gitReviewDiffCopied.
  ///
  /// In en, this message translates to:
  /// **'Diff copied'**
  String get gitReviewDiffCopied;

  /// No description provided for @gitReviewSave.
  ///
  /// In en, this message translates to:
  /// **'Save changes'**
  String get gitReviewSave;

  /// No description provided for @gitReviewSaveUnavailable.
  ///
  /// In en, this message translates to:
  /// **'Only managed isolated workspaces can be saved from mobile'**
  String get gitReviewSaveUnavailable;

  /// No description provided for @gitReviewSaveTitle.
  ///
  /// In en, this message translates to:
  /// **'Save all current changes?'**
  String get gitReviewSaveTitle;

  /// No description provided for @gitReviewSaveHint.
  ///
  /// In en, this message translates to:
  /// **'Hoxi checks file safety and saves every current workspace change in a Git commit.'**
  String get gitReviewSaveHint;

  /// No description provided for @gitReviewMessage.
  ///
  /// In en, this message translates to:
  /// **'Commit message'**
  String get gitReviewMessage;

  /// No description provided for @gitReviewMessageHint.
  ///
  /// In en, this message translates to:
  /// **'Leave blank to use the default message'**
  String get gitReviewMessageHint;

  /// No description provided for @gitReviewCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get gitReviewCancel;

  /// No description provided for @gitReviewConfirmSave.
  ///
  /// In en, this message translates to:
  /// **'Save'**
  String get gitReviewConfirmSave;

  /// No description provided for @gitReviewSaveSuccess.
  ///
  /// In en, this message translates to:
  /// **'Changes saved in a Git commit'**
  String get gitReviewSaveSuccess;

  /// No description provided for @gitReviewSaveFailed.
  ///
  /// In en, this message translates to:
  /// **'Save failed. Review the error and try again.'**
  String get gitReviewSaveFailed;

  /// No description provided for @codeFlowTitle.
  ///
  /// In en, this message translates to:
  /// **'Delivery preview'**
  String get codeFlowTitle;

  /// No description provided for @codeFlowStartHint.
  ///
  /// In en, this message translates to:
  /// **'Build this delivery\'s commits and deploy them to the preview environment for acceptance.'**
  String get codeFlowStartHint;

  /// No description provided for @codeFlowAutoStartHint.
  ///
  /// In en, this message translates to:
  /// **'Previews start automatically after delivery.'**
  String get codeFlowAutoStartHint;

  /// No description provided for @codeFlowAutoStartPending.
  ///
  /// In en, this message translates to:
  /// **'Delivery finished. Generating the preview automatically…'**
  String get codeFlowAutoStartPending;

  /// No description provided for @codeFlowStart.
  ///
  /// In en, this message translates to:
  /// **'Generate preview'**
  String get codeFlowStart;

  /// No description provided for @codeFlowStarted.
  ///
  /// In en, this message translates to:
  /// **'Preview started'**
  String get codeFlowStarted;

  /// No description provided for @codeFlowOpenPreview.
  ///
  /// In en, this message translates to:
  /// **'Open preview'**
  String get codeFlowOpenPreview;

  /// No description provided for @codeFlowVersion.
  ///
  /// In en, this message translates to:
  /// **'v{version}'**
  String codeFlowVersion(String version);

  /// No description provided for @codeFlowStatusQueued.
  ///
  /// In en, this message translates to:
  /// **'Queued'**
  String get codeFlowStatusQueued;

  /// No description provided for @codeFlowStatusRunning.
  ///
  /// In en, this message translates to:
  /// **'Running'**
  String get codeFlowStatusRunning;

  /// No description provided for @codeFlowStatusSuccess.
  ///
  /// In en, this message translates to:
  /// **'Released'**
  String get codeFlowStatusSuccess;

  /// No description provided for @codeFlowStatusFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed'**
  String get codeFlowStatusFailed;

  /// No description provided for @codeFlowStatusWaitingDeployment.
  ///
  /// In en, this message translates to:
  /// **'Awaiting release'**
  String get codeFlowStatusWaitingDeployment;

  /// No description provided for @codeFlowStageBuilding.
  ///
  /// In en, this message translates to:
  /// **'Building'**
  String get codeFlowStageBuilding;

  /// No description provided for @codeFlowStagePublishing.
  ///
  /// In en, this message translates to:
  /// **'Publishing release'**
  String get codeFlowStagePublishing;

  /// No description provided for @codeFlowStageDeploying.
  ///
  /// In en, this message translates to:
  /// **'Deploying'**
  String get codeFlowStageDeploying;

  /// No description provided for @codeFlowEnvironmentPreview.
  ///
  /// In en, this message translates to:
  /// **'Preview'**
  String get codeFlowEnvironmentPreview;

  /// No description provided for @codeFlowEnvironmentProduction.
  ///
  /// In en, this message translates to:
  /// **'Production'**
  String get codeFlowEnvironmentProduction;

  /// No description provided for @codeFlowStatePending.
  ///
  /// In en, this message translates to:
  /// **'Pending'**
  String get codeFlowStatePending;

  /// No description provided for @codeFlowStateDeploying.
  ///
  /// In en, this message translates to:
  /// **'Deploying'**
  String get codeFlowStateDeploying;

  /// No description provided for @codeFlowStateFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed'**
  String get codeFlowStateFailed;

  /// No description provided for @codeFlowStateCurrent.
  ///
  /// In en, this message translates to:
  /// **'Live'**
  String get codeFlowStateCurrent;

  /// No description provided for @codeFlowStateReplaced.
  ///
  /// In en, this message translates to:
  /// **'Replaced'**
  String get codeFlowStateReplaced;

  /// No description provided for @codeFlowActionDeploy.
  ///
  /// In en, this message translates to:
  /// **'Deploy'**
  String get codeFlowActionDeploy;

  /// No description provided for @codeFlowActionRelease.
  ///
  /// In en, this message translates to:
  /// **'Release'**
  String get codeFlowActionRelease;

  /// No description provided for @codeFlowActionRedeploy.
  ///
  /// In en, this message translates to:
  /// **'Redeploy'**
  String get codeFlowActionRedeploy;

  /// No description provided for @codeFlowActionSwitchBack.
  ///
  /// In en, this message translates to:
  /// **'Switch back'**
  String get codeFlowActionSwitchBack;

  /// No description provided for @codeFlowConfirmTitle.
  ///
  /// In en, this message translates to:
  /// **'Deploy to {environment}?'**
  String codeFlowConfirmTitle(String environment);

  /// No description provided for @codeFlowConfirmProduction.
  ///
  /// In en, this message translates to:
  /// **'Release v{version} to production ({website}). This switches live traffic. A health check runs before switching, and the previous version is restored automatically if it fails during stabilization.'**
  String codeFlowConfirmProduction(String version, String website);

  /// No description provided for @codeFlowConfirm.
  ///
  /// In en, this message translates to:
  /// **'Deploy'**
  String get codeFlowConfirm;

  /// No description provided for @codeFlowCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get codeFlowCancel;

  /// No description provided for @codeFlowDeployQueued.
  ///
  /// In en, this message translates to:
  /// **'Deployment queued'**
  String get codeFlowDeployQueued;

  /// No description provided for @codeFlowBlockedRunnerTargetStale.
  ///
  /// In en, this message translates to:
  /// **'A newer build has replaced this version\'s runtime. Rebuild it on desktop before deploying.'**
  String get codeFlowBlockedRunnerTargetStale;

  /// No description provided for @codeFlowBlockedRunnerTargetUnavailable.
  ///
  /// In en, this message translates to:
  /// **'This version\'s Runner container was cleaned up. Rebuild it on desktop before deploying.'**
  String get codeFlowBlockedRunnerTargetUnavailable;

  /// No description provided for @codeFlowBlockedRunnerFixedPort.
  ///
  /// In en, this message translates to:
  /// **'The pipeline uses a fixed host port, so preview and production cannot coexist. Switch to automatic ports on desktop.'**
  String get codeFlowBlockedRunnerFixedPort;

  /// No description provided for @codeFlowOpenFailed.
  ///
  /// In en, this message translates to:
  /// **'Unable to open the preview URL'**
  String get codeFlowOpenFailed;

  /// No description provided for @authEntranceInvalid.
  ///
  /// In en, this message translates to:
  /// **'Use only letters, numbers, underscores, and hyphens'**
  String get authEntranceInvalid;

  /// No description provided for @authOperationTimeout.
  ///
  /// In en, this message translates to:
  /// **'The operation timed out. Check the network or keychain access.'**
  String get authOperationTimeout;

  /// No description provided for @authConnectFailed.
  ///
  /// In en, this message translates to:
  /// **'Connection or sign-in failed: {detail}'**
  String authConnectFailed(String detail);

  /// No description provided for @authQrVerifyFailed.
  ///
  /// In en, this message translates to:
  /// **'QR authorization failed: {detail}'**
  String authQrVerifyFailed(String detail);

  /// No description provided for @authSwitchFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed to switch server: {detail}'**
  String authSwitchFailed(String detail);

  /// No description provided for @codeWsModelPickerSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search model name, provider, or ID…'**
  String get codeWsModelPickerSearchHint;

  /// No description provided for @codeWsModelPickerCustomInput.
  ///
  /// In en, this message translates to:
  /// **'Custom model ID'**
  String get codeWsModelPickerCustomInput;

  /// No description provided for @codeWsModelPickerCustomHint.
  ///
  /// In en, this message translates to:
  /// **'e.g. anthropic/claude-sonnet-4-6'**
  String get codeWsModelPickerCustomHint;

  /// No description provided for @codeWsModelPickerEmpty.
  ///
  /// In en, this message translates to:
  /// **'No matching models found'**
  String get codeWsModelPickerEmpty;

  /// No description provided for @codeWsModelPickerDefaultSubtitle.
  ///
  /// In en, this message translates to:
  /// **'Follow current executor default configuration'**
  String get codeWsModelPickerDefaultSubtitle;

  /// No description provided for @codeWsProjectPickerSearchHint.
  ///
  /// In en, this message translates to:
  /// **'Search project name or path…'**
  String get codeWsProjectPickerSearchHint;

  /// No description provided for @codeWsProjectPickerEmpty.
  ///
  /// In en, this message translates to:
  /// **'No matching projects found'**
  String get codeWsProjectPickerEmpty;

  /// No description provided for @chatCameraPermissionDenied.
  ///
  /// In en, this message translates to:
  /// **'Camera or photo library permission denied. No photo was sent.'**
  String get chatCameraPermissionDenied;

  /// No description provided for @chatActionPickGallery.
  ///
  /// In en, this message translates to:
  /// **'Choose from Photo Library'**
  String get chatActionPickGallery;

}

class _AppLocalizationsDelegate
    extends LocalizationsDelegate<AppLocalizations> {
  const _AppLocalizationsDelegate();

  @override
  Future<AppLocalizations> load(Locale locale) {
    return SynchronousFuture<AppLocalizations>(lookupAppLocalizations(locale));
  }

  @override
  bool isSupported(Locale locale) =>
      <String>['en', 'zh'].contains(locale.languageCode);

  @override
  bool shouldReload(_AppLocalizationsDelegate old) => false;
}

AppLocalizations lookupAppLocalizations(Locale locale) {
  // Lookup logic when only language code is specified.
  switch (locale.languageCode) {
    case 'en':
      return AppLocalizationsEn();
    case 'zh':
      return AppLocalizationsZh();
  }

  throw FlutterError(
    'AppLocalizations.delegate failed to load unsupported locale "$locale". This is likely '
    'an issue with the localizations generation tool. Please file an issue '
    'on GitHub with a reproducible sample app and the gen-l10n configuration '
    'that was used.',
  );
}
