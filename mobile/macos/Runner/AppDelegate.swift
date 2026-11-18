import Cocoa
import FlutterMacOS
import UserNotifications

@main
class AppDelegate: FlutterAppDelegate, UNUserNotificationCenterDelegate {
  private var pushChannel: FlutterMethodChannel?
  private var pendingPushTap: [String: Any]?

  override func applicationDidFinishLaunching(_ notification: Notification) {
    super.applicationDidFinishLaunching(notification)
    UNUserNotificationCenter.current().delegate = self
    if let iconPath = Bundle.main.path(forResource: "AppIcon", ofType: "icns"),
       let icon = NSImage(contentsOfFile: iconPath) {
      NSApplication.shared.applicationIconImage = icon
    }
    if let controller = mainFlutterWindow?.contentViewController as? FlutterViewController {
      let channel = FlutterMethodChannel(
        name: "hoxi.ai/push",
        binaryMessenger: controller.engine.binaryMessenger
      )
      pushChannel = channel
      channel.setMethodCallHandler { [weak self] call, result in
        self?.handlePushCall(call, result: result)
      }
    }
  }

  private func handlePushCall(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    switch call.method {
    case "getPermissionStatus":
      UNUserNotificationCenter.current().getNotificationSettings { settings in
        DispatchQueue.main.async {
          switch settings.authorizationStatus {
          case .authorized, .provisional:
            result("granted")
          case .denied:
            result("denied")
          case .notDetermined:
            result("notDetermined")
          @unknown default:
            result("notDetermined")
          }
        }
      }
    case "requestPermission":
      UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
        DispatchQueue.main.async { result(granted) }
      }
    case "openSettings":
      if let url = URL(string: "x-apple.systempreferences:com.apple.preference.notifications") {
        NSWorkspace.shared.open(url)
        result(true)
      } else {
        result(false)
      }
    case "register":
      UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
        DispatchQueue.main.async {
          result(granted ? "macos_dev_token" : nil)
        }
      }
    case "unregister":
      result(true)
    case "showLocalNotification":
      guard let args = call.arguments as? [String: Any] else {
        result(false)
        return
      }
      let identifier = (args["id"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? UUID().uuidString
      let title = (args["title"] as? String) ?? ""
      let body = (args["body"] as? String) ?? ""
      let playSound = (args["sound"] as? Bool) ?? true
      let payload = (args["payload"] as? [String: Any]) ?? [:]
      let content = UNMutableNotificationContent()
      content.title = title
      content.body = body
      if playSound { content.sound = .default }
      content.userInfo = ["hoxi": payload]
      let req = UNNotificationRequest(
        identifier: identifier.isEmpty ? UUID().uuidString : identifier,
        content: content,
        trigger: nil
      )
      UNUserNotificationCenter.current().add(req) { error in
        DispatchQueue.main.async { result(error == nil) }
      }
    case "clearNotifications":
      UNUserNotificationCenter.current().removeAllDeliveredNotifications()
      result(true)
    case "takeTap":
      result(pendingPushTap)
      pendingPushTap = nil
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    if #available(macOS 11.0, *) {
      completionHandler([.banner, .list, .sound])
    } else {
      completionHandler([.alert, .sound])
    }
  }

  func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    didReceive response: UNNotificationResponse,
    withCompletionHandler completionHandler: @escaping () -> Void
  ) {
    if let hoxi = response.notification.request.content.userInfo["hoxi"] as? [String: Any] {
      pendingPushTap = hoxi
      pushChannel?.invokeMethod("tapAvailable", arguments: nil)
    }
    completionHandler()
  }

  override func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    return true
  }

  override func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
    return true
  }
}
