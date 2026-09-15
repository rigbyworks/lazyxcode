#!/usr/bin/env python3
"""Create a disposable iOS app and XCTest project for release verification."""
import json
from pathlib import Path
import sys

root = Path(sys.argv[1])
root.mkdir(parents=True, exist_ok=True)
objects = {}

def add(isa, **values):
    key = f'{len(objects) + 1:024X}'
    objects[key] = dict(isa=isa, **values)
    return key

def configs(settings):
    entries = [add('XCBuildConfiguration', name=name, buildSettings=settings)
               for name in ['Debug', 'Release']]
    return add('XCConfigurationList', buildConfigurations=entries,
               defaultConfigurationIsVisible='0', defaultConfigurationName='Debug')

def source(name, content):
    (root / name).write_text(content)
    return add('PBXFileReference', lastKnownFileType='sourcecode.swift', path=name, sourceTree='<group>')

app_source = source('Smoke.swift', '''import SwiftUI
final class Counter {
    func increment(_ value: Int) -> Int { value + 1 }
}
@main struct SmokeApp: App {
    init() { print("LAZYXCODE_SMOKE_READY") }
    var body: some Scene { WindowGroup { Text("lazyxcode release smoke test") } }
}
''')
test_source = source('SmokeTests.swift', '''import XCTest
@testable import Smoke
final class SmokeTests: XCTestCase {
    func testIncrement() { XCTAssertEqual(Counter().increment(1), 2) }
}
''')
app_product = add('PBXFileReference', explicitFileType='wrapper.application', path='Smoke.app', sourceTree='BUILT_PRODUCTS_DIR')
test_product = add('PBXFileReference', explicitFileType='wrapper.cfbundle', path='SmokeTests.xctest', sourceTree='BUILT_PRODUCTS_DIR')
products = add('PBXGroup', children=[app_product, test_product], name='Products', sourceTree='<group>')
group = add('PBXGroup', children=[app_source, test_source, products], sourceTree='<group>')
project_configs = configs(dict(SDKROOT='iphoneos', IPHONEOS_DEPLOYMENT_TARGET='16.0', SWIFT_VERSION='5.0', CODE_SIGNING_ALLOWED='NO', ENABLE_TESTABILITY='YES', SWIFT_OPTIMIZATION_LEVEL='-Onone'))

def target(name, src, product, kind, extra):
    build_file = add('PBXBuildFile', fileRef=src)
    phases = [add('PBXSourcesBuildPhase', buildActionMask='2147483647', files=[build_file], runOnlyForDeploymentPostprocessing='0'),
              add('PBXFrameworksBuildPhase', buildActionMask='2147483647', files=[], runOnlyForDeploymentPostprocessing='0'),
              add('PBXResourcesBuildPhase', buildActionMask='2147483647', files=[], runOnlyForDeploymentPostprocessing='0')]
    settings = dict(PRODUCT_BUNDLE_IDENTIFIER='com.rigbyworks.lazyxcode.' + name.lower(), PRODUCT_NAME='$(TARGET_NAME)', GENERATE_INFOPLIST_FILE='YES', TARGETED_DEVICE_FAMILY='1,2', **extra)
    return add('PBXNativeTarget', name=name, productName=name, productReference=product,
               productType=kind, buildConfigurationList=configs(settings), buildPhases=phases, buildRules=[], dependencies=[])

app = target('Smoke', app_source, app_product, 'com.apple.product-type.application', {'INFOPLIST_KEY_UILaunchScreen_Generation': 'YES'})
tests = target('SmokeTests', test_source, test_product, 'com.apple.product-type.bundle.unit-test', {'TEST_HOST': '$(BUILT_PRODUCTS_DIR)/Smoke.app/Smoke', 'BUNDLE_LOADER': '$(TEST_HOST)'})
project = add('PBXProject', buildConfigurationList=project_configs, compatibilityVersion='Xcode 14.0', developmentRegion='en', hasScannedForEncodings='0', knownRegions=['en', 'Base'], mainGroup=group, productRefGroup=products, projectDirPath='', projectRoot='', targets=[app, tests])
proxy = add('PBXContainerItemProxy', containerPortal=project, proxyType='1', remoteGlobalIDString=app, remoteInfo='Smoke')
objects[tests]['dependencies'] = [add('PBXTargetDependency', target=app, targetProxy=proxy)]

def encode(value):
    if isinstance(value, dict):
        return '{\n' + '\n'.join(f'{json.dumps(k)} = {encode(v)};' for k, v in value.items()) + '\n}'
    if isinstance(value, list):
        return '(' + ','.join(encode(v) for v in value) + ')'
    return json.dumps(value)

bundle = root / 'Smoke.xcodeproj'
schemes = bundle / 'xcshareddata/xcschemes'
schemes.mkdir(parents=True, exist_ok=True)
(bundle / 'project.pbxproj').write_text('// !$*UTF8*$!\n' + encode(dict(archiveVersion='1', classes={}, objectVersion='56', objects=objects, rootObject=project)))
def ref(identifier, name):
    return f'<BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="{identifier}" BuildableName="{name}" BlueprintName="{name.split(".")[0]}" ReferencedContainer="container:Smoke.xcodeproj"/>'
(schemes / 'Smoke.xcscheme').write_text(f'''<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion="1630" version="1.3">
<BuildAction parallelizeBuildables="YES" buildImplicitDependencies="YES"><BuildActionEntries>
<BuildActionEntry buildForTesting="YES" buildForRunning="YES" buildForProfiling="YES" buildForArchiving="YES" buildForAnalyzing="YES">{ref(app, 'Smoke.app')}</BuildActionEntry>
</BuildActionEntries></BuildAction>
<TestAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.IDEFoundation.Launcher.LLDB" shouldUseLaunchSchemeArgsEnv="YES" codeCoverageEnabled="YES"><Testables><TestableReference skipped="NO">{ref(tests, 'SmokeTests.xctest')}</TestableReference></Testables></TestAction>
<LaunchAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.IDEFoundation.Launcher.LLDB"><BuildableProductRunnable runnableDebuggingMode="0">{ref(app, 'Smoke.app')}</BuildableProductRunnable></LaunchAction>
</Scheme>''')
