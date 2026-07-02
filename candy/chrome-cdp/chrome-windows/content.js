(function() {
  "use strict";

  Object.defineProperty(navigator, "platform", {
    get: function() { return "Win32"; },
    configurable: true
  });

  if (navigator.userAgentData) {
    var real = navigator.userAgentData;
    var spoofed = {
      brands: real.brands,
      mobile: real.mobile,
      platform: "Windows",
      getHighEntropyValues: function(hints) {
        return real.getHighEntropyValues(hints).then(function(v) {
          v.platform = "Windows";
          v.platformVersion = "15.0.0";
          return v;
        });
      },
      toJSON: function() {
        var j = real.toJSON();
        j.platform = "Windows";
        return j;
      }
    };
    Object.defineProperty(navigator, "userAgentData", {
      get: function() { return spoofed; },
      configurable: true
    });
  }
})();
